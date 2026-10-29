package main

import (
	"github.com/ozal-project/ozal/internal/config"
	"net/netip"
)

func parseTunnelNetwork(value string) (netip.Prefix, error) { return config.ParseTunnelNetwork(value) }
func validateTunnelAddress(value string, allowZero bool, network netip.Prefix) error {
	return config.ValidateTunnelAddress(value, allowZero, network)
}
