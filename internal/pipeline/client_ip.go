package pipeline

import (
	"context"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

type clientIPKey struct{}

// ClientIP returns the address selected by Trust. RemoteAddr remains the
// actual transport peer; consumers must not reinterpret forwarding headers.
func ClientIP(ctx context.Context) (netip.Addr, bool) {
	ip, ok := ctx.Value(clientIPKey{}).(netip.Addr)
	return ip, ok && ip.IsValid()
}

// TrustWithProxies captures an immutable peer allowlist for this snapshot.
// Only one valid CF-Connecting-IP from a trusted TCP peer is accepted.
func TrustWithProxies(proxies []netip.Prefix) Stage {
	trusted := slices.Clone(proxies)
	return trustStage(func(r *http.Request) netip.Addr {
		return resolveClientIP(r.RemoteAddr, headerValues(r.Header, "CF-Connecting-IP"), trusted)
	})
}

// TrustAuthenticatedTunnel is the connector side of the tunnel: every request
// arrives through the mutually authenticated tunnel, so the edge on the other
// end is the only party that can have set VisitorIPHeader. Use it only on a
// handler that is reachable exclusively through that tunnel.
func TrustAuthenticatedTunnel() Stage {
	return trustStage(func(r *http.Request) netip.Addr {
		return resolveTunnelClientIP(r.RemoteAddr, headerValues(r.Header, VisitorIPHeader))
	})
}

// trustStage runs resolve once on the original headers, then strips every
// spoofable header, so nothing after it can read a client-supplied value.
func trustStage(resolve func(*http.Request) netip.Addr) Stage {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// An outer Trust already resolved the address from the original
			// headers; running again would see them stripped and downgrade
			// the client to the transport peer.
			ip, resolved := ClientIP(r.Context())
			if !resolved {
				ip = resolve(r)
			}
			stripIdentityHeaders(r.Header)
			stripForwardingHeaders(r.Header)
			if !resolved && ip.IsValid() {
				r = r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func resolveClientIP(remoteAddr string, forwarded []string, trusted []netip.Prefix) netip.Addr {
	peer, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	ip := peer.Addr().Unmap().WithZone("")
	if len(forwarded) != 1 {
		return ip
	}
	for _, prefix := range trusted {
		if !prefix.Contains(ip) {
			continue
		}
		if client, ok := parseVisitorAddr(forwarded[0]); ok {
			return client
		}
		break
	}
	return ip
}

// resolveTunnelClientIP prefers the edge's single valid assertion and falls
// back to the tunnel peer, so a malformed or repeated claim degrades safely.
func resolveTunnelClientIP(remoteAddr string, asserted []string) netip.Addr {
	if len(asserted) == 1 {
		if client, ok := parseVisitorAddr(asserted[0]); ok {
			return client
		}
	}
	peer, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	return peer.Addr().Unmap().WithZone("")
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
		case "forwarded", "x-real-ip", "true-client-ip", "cf-connecting-ip", "cf-connecting-ipv6", "cf-pseudo-ipv4", "x-switchboard-visitor-ip":
			delete(h, name)
		}
	}
}
