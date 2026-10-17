package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// ParseTrustedProxies normalizes IP literals and CIDRs into network prefixes.
// Hostnames are rejected: trust must not depend on DNS changes.
func ParseTrustedProxies(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		raw := strings.TrimSpace(value)
		addr, err := netip.ParseAddr(raw)
		if err == nil && addr.Zone() == "" {
			addr = addr.Unmap()
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("config: trusted_proxies entry %q must be an IP or CIDR", value)
		}
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return nil, fmt.Errorf("config: trusted_proxies mapped IPv4 prefix %q must be /96 or narrower", value)
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}
