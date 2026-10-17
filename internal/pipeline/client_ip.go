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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// An outer Trust already resolved the address from the original
			// headers; running again would see them stripped and downgrade
			// the client to the transport peer.
			ip, resolved := ClientIP(r.Context())
			if !resolved {
				ip = resolveClientIP(r.RemoteAddr, connectingIPValues(r.Header), trusted)
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
		client, err := netip.ParseAddr(strings.TrimSpace(forwarded[0]))
		if err == nil && client.Zone() == "" && !client.IsUnspecified() && !client.IsMulticast() {
			return client.Unmap()
		}
		break
	}
	return ip
}

func connectingIPValues(h http.Header) []string {
	var values []string
	for name, entries := range h {
		if strings.EqualFold(strings.ReplaceAll(name, "_", "-"), "CF-Connecting-IP") {
			// An underscore alias is never an authoritative header. Reject
			// the whole claim if present, even alongside a canonical value.
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
		case "forwarded", "x-real-ip", "true-client-ip", "cf-connecting-ip", "cf-connecting-ipv6", "cf-pseudo-ipv4":
			delete(h, name)
		}
	}
}
