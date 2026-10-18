package main

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
)

// parseTunnelNetwork accepts a subnet wholly inside RFC1918 or IPv6 ULA.
// A broad prefix containing both private and public addresses is rejected.
func parseTunnelNetwork(value string) (netip.Prefix, error) {
	if value == "" {
		return netip.Prefix{}, nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err == nil && prefix == prefix.Masked() {
		for _, block := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"} {
			private := netip.MustParsePrefix(block)
			if prefix.Bits() >= private.Bits() && private.Contains(prefix.Addr()) {
				return prefix, nil
			}
		}
	}
	return netip.Prefix{}, errors.New("-tunnel-network must be a canonical CIDR wholly inside a private network")
}

func validateTunnelAddress(value string, allowZero bool, network netip.Prefix) error {
	if !network.IsValid() {
		return validateLabAddress(value, allowZero)
	}
	host, port, err := net.SplitHostPort(value)
	ip, ipErr := netip.ParseAddr(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || ipErr != nil || ip.Zone() != "" || portErr != nil || n < 0 || n > 65535 || (!allowZero && n == 0) {
		return errors.New("tunnel address must be a literal IP and valid port")
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || network.Contains(ip) {
		return nil
	}
	return errors.New("tunnel address must be loopback or inside -tunnel-network")
}
