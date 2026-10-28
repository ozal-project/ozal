package pipeline

import (
	"context"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/switchboard-project/switchboard/internal/policycontext"
)

type clientIPKey struct{}
type trustEvidenceKey struct{}

// TrustEvidence is the source-level provenance selected by the trust stage.
// It contains no client address, so it can be attached to a policy decision
// without duplicating personal network data.
type TrustEvidence struct {
	ArrivalPath     string
	VisitorIPSource string
}

type trustDecision struct {
	ClientIP netip.Addr
	TrustEvidence
}

// ClientIP returns the address selected by Trust. RemoteAddr remains the
// actual transport peer; consumers must not reinterpret forwarding headers.
func ClientIP(ctx context.Context) (netip.Addr, bool) {
	ip, ok := ctx.Value(clientIPKey{}).(netip.Addr)
	return ip, ok && ip.IsValid()
}

// TrustEvidenceFrom reports the arrival path and source of the selected
// visitor IP. It deliberately does not return the address itself.
func TrustEvidenceFrom(ctx context.Context) (TrustEvidence, bool) {
	evidence, ok := ctx.Value(trustEvidenceKey{}).(TrustEvidence)
	return evidence, ok
}

// TrustWithProxies captures an immutable peer allowlist for this snapshot.
// Only one valid CF-Connecting-IP from a trusted TCP peer is accepted.
func TrustWithProxies(proxies []netip.Prefix) Stage {
	trusted := slices.Clone(proxies)
	return trustStage(func(r *http.Request) trustDecision {
		return resolveClientEvidence(r.RemoteAddr, headerValues(r.Header, "CF-Connecting-IP"), trusted)
	})
}

// TrustAuthenticatedTunnel is the connector side of the tunnel: every request
// arrives through the mutually authenticated tunnel, so the edge on the other
// end is the only party that can have set VisitorIPHeader or the visitor
// identity headers. Use it only on a handler that is reachable exclusively
// through that tunnel.
func TrustAuthenticatedTunnel() Stage {
	address := trustStage(func(r *http.Request) trustDecision {
		return resolveTunnelEvidence(r.RemoteAddr, headerValues(r.Header, VisitorIPHeader))
	})
	return func(next http.Handler) http.Handler {
		inner := address(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Read before the trust stage strips the headers.
			users := headerValues(r.Header, VisitorUserHeader)
			emails := headerValues(r.Header, VisitorEmailHeader)
			if v, ok := parseVisitorIdentity(users, emails); ok {
				r = r.WithContext(WithVisitor(r.Context(), v))
			}
			inner.ServeHTTP(w, r)
		})
	}
}

// trustStage runs resolve once on the original headers, then strips every
// spoofable header, so nothing after it can read a client-supplied value.
func trustStage(resolve func(*http.Request) trustDecision) Stage {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// An outer Trust already resolved the address from the original
			// headers; running again would see them stripped and downgrade
			// the client to the transport peer.
			evidence, hasEvidence := TrustEvidenceFrom(r.Context())
			decision := trustDecision{TrustEvidence: evidence}
			if ip, ok := ClientIP(r.Context()); ok {
				decision.ClientIP = ip
			}
			if !hasEvidence && !decision.ClientIP.IsValid() {
				decision = resolve(r)
			}
			stripIdentityHeaders(r.Header)
			stripForwardingHeaders(r.Header)
			ctx := context.WithValue(r.Context(), trustEvidenceKey{}, decision.TrustEvidence)
			if decision.ClientIP.IsValid() {
				ctx = context.WithValue(ctx, clientIPKey{}, decision.ClientIP)
			}
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func resolveClientEvidence(remoteAddr string, forwarded []string, trusted []netip.Prefix) trustDecision {
	peer, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return trustDecision{}
	}
	ip := peer.Addr().Unmap().WithZone("")
	evidence := trustDecision{ClientIP: ip, TrustEvidence: TrustEvidence{ArrivalPath: policycontext.ArrivalDirect, VisitorIPSource: policycontext.IPSourcePeer}}
	trustedPeer := false
	for _, prefix := range trusted {
		if prefix.Contains(ip) {
			trustedPeer = true
			break
		}
	}
	if !trustedPeer {
		return evidence
	}
	evidence.TrustEvidence.ArrivalPath = policycontext.ArrivalProxy
	if len(forwarded) == 1 {
		if client, ok := parseVisitorAddr(forwarded[0]); ok {
			evidence.ClientIP = client
			evidence.TrustEvidence.VisitorIPSource = policycontext.IPSourceProxy
		}
	}
	return evidence
}

// resolveTunnelEvidence prefers the edge's single valid assertion and falls
// back to the tunnel peer, so a malformed or repeated claim degrades safely.
func resolveTunnelEvidence(remoteAddr string, asserted []string) trustDecision {
	evidence := trustDecision{TrustEvidence: TrustEvidence{ArrivalPath: policycontext.ArrivalTunnel, VisitorIPSource: policycontext.IPSourcePeer}}
	if len(asserted) == 1 {
		if client, ok := parseVisitorAddr(asserted[0]); ok {
			evidence.ClientIP = client
			evidence.TrustEvidence.VisitorIPSource = policycontext.IPSourceTunnel
			return evidence
		}
	}
	peer, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return evidence
	}
	evidence.ClientIP = peer.Addr().Unmap().WithZone("")
	return evidence
}

// parseVisitorAddr accepts one plain unicast address, as a visitor address
// from any header must be: no zones, no wildcard, no multicast.
func parseVisitorAddr(value string) (netip.Addr, bool) {
	client, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || client.Zone() != "" || client.IsUnspecified() || client.IsMulticast() {
		return netip.Addr{}, false
	}
	return client.Unmap(), true
}

// headerValues returns every value of the named header in any "-" or "_"
// spelling. An underscore spelling is never authoritative: it rejects the whole
// claim, even next to a canonical value.
func headerValues(h http.Header, canonical string) []string {
	var values []string
	for name, entries := range h {
		if strings.EqualFold(strings.ReplaceAll(name, "_", "-"), canonical) {
			if strings.Contains(name, "_") {
				return nil
			}
			values = append(values, entries...)
		}
	}
	return values
}

func stripForwardingHeaders(h http.Header) {
	for name := range h {
		normalized := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
		if strings.HasPrefix(normalized, "x-forwarded-") {
			delete(h, name)
			continue
		}
		switch normalized {
		case "forwarded", "x-real-ip", "true-client-ip", "cf-connecting-ip", "cf-connecting-ipv6", "cf-pseudo-ipv4",
			"x-switchboard-visitor-ip", "x-switchboard-visitor-user", "x-switchboard-visitor-email":
			delete(h, name)
		}
	}
}
