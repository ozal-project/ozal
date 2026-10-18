package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"strconv"
)

// ParseTunnelNetwork accepts a subnet wholly inside RFC1918 or IPv6 ULA.
// A broad prefix containing both private and public addresses is rejected.
func ParseTunnelNetwork(value string) (netip.Prefix, error) {
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

func ValidateTunnelAddress(value string, allowZero bool, network netip.Prefix) error {
	if !network.IsValid() {
		host, port, err := net.SplitHostPort(value)
		ip := net.ParseIP(host)
		n, portErr := strconv.Atoi(port)
		if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || n < 0 || n > 65535 || (!allowZero && n == 0) {
			return errors.New("tunnel address must be a literal loopback IP and valid port")
		}
		return nil
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

// Tunnel configures the registry-backed transport in the main edge process.
// Listener/certificate changes require a restart; route mappings reload atomically.
type Tunnel struct {
	Listen   string `json:"listen"`
	Network  string `json:"network,omitempty"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
	CAFile   string `json:"ca_file"`
}

var connectorNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateConnectorName shares the registry's name grammar with route parsing.
func ValidateConnectorName(name string) error {
	if !connectorNamePattern.MatchString(name) {
		return errors.New("connector name must be 1-63 characters of a-z, 0-9 or '-' with alphanumeric ends")
	}
	return nil
}

func validateTunnel(cfg *Tunnel) error {
	if cfg == nil {
		return nil
	}
	network, err := ParseTunnelNetwork(cfg.Network)
	if err != nil {
		return fmt.Errorf("config: tunnel.network: %w", err)
	}
	if err := ValidateTunnelAddress(cfg.Listen, true, network); err != nil {
		return fmt.Errorf("config: tunnel.listen: %w", err)
	}
	for _, field := range []struct{ name, value string }{{"cert_file", cfg.CertFile}, {"key_file", cfg.KeyFile}, {"ca_file", cfg.CAFile}} {
		if !filepath.IsAbs(field.value) {
			return fmt.Errorf("config: tunnel.%s must be an absolute file path", field.name)
		}
	}
	return nil
}
