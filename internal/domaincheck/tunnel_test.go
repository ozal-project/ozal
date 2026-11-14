package domaincheck

import (
	"net/netip"
	"strings"
	"testing"
)

func TestAssessTunnel(t *testing.T) {
	const target = "c0ffee00.cfargotunnel.com"
	cloudflareAddr := []netip.Addr{netip.MustParseAddr("104.16.0.1")}
	for _, tc := range []struct {
		name                     string
		target                   string
		o                        Observation
		dns, reach, cert         string
		reachMention, dnsMention string
	}{
		{"reached through the tunnel", target, Observation{Lookup: LookupFound, Addresses: cloudflareAddr, Reach: ReachEdge, Status: 200}, StateOK, StateOK, StateOK, "", "Cloudflare"},
		{"no connector (530)", target, Observation{Lookup: LookupFound, Addresses: cloudflareAddr, Reach: ReachProxyOrigin, Status: 530}, StateOK, StateBad, StateOK, "Start cloudflared", ""},
		{"connector cannot reach Ozal (502)", target, Observation{Lookup: LookupFound, Addresses: cloudflareAddr, Reach: ReachOther, Status: 502}, StateWait, StateBad, StateOK, "listener", target},
		{"old hosting", target, Observation{Lookup: LookupFound, Addresses: []netip.Addr{netip.MustParseAddr("203.0.113.5")}, Reach: ReachOther, Status: 200}, StateWait, StateBad, StateOK, "Another server", target},
		{"tunnel not created yet", "", Observation{Lookup: LookupFound, Addresses: cloudflareAddr, Reach: ReachNone}, StateWait, StateBad, StateWait, "", "Create the Cloudflare Tunnel"},
		{"no record", target, Observation{Lookup: LookupNotFound, Reach: ReachNone}, StateBad, StateWait, StateWait, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := AssessTunnel(Assess("app.example.com", tc.target, true, tc.o), tc.target, tc.o)
			if a.DNS.State != tc.dns || a.Reach.State != tc.reach || a.Certificate.State != tc.cert {
				t.Fatalf("states dns=%s reach=%s cert=%s (%+v)", a.DNS.State, a.Reach.State, a.Certificate.State, a)
			}
			if tc.reachMention != "" && !strings.Contains(a.Reach.Detail, tc.reachMention) {
				t.Fatalf("reach detail %q lacks %q", a.Reach.Detail, tc.reachMention)
			}
			if tc.dnsMention != "" && !strings.Contains(a.DNS.Detail, tc.dnsMention) {
				t.Fatalf("dns detail %q lacks %q", a.DNS.Detail, tc.dnsMention)
			}
			if a.Connected != (tc.o.Reach == ReachEdge) {
				t.Fatal("Connected changed")
			}
		})
	}
}
