package domaincheck

import (
	"net/netip"
	"testing"
)

func TestInstructionNamesOneRecordForTheTarget(t *testing.T) {
	for _, tc := range []struct{ host, target, typ, name string }{
		{"testing.example.com", "203.0.113.10", "A", "testing.example.com"},
		{"testing.example.com", "2001:db8::1", "AAAA", "testing.example.com"},
		{"testing.example.com", "edge.example.net", "CNAME", "testing.example.com"},
		{"local.test:8888", "198.51.100.2", "A", "local.test"},
	} {
		record, ok := Instruction(tc.host, tc.target, true)
		if !ok || record.Type != tc.typ || record.Name != tc.name || record.Content != tc.target || !record.Proxied {
			t.Errorf("Instruction(%q, %q) = %+v, %v", tc.host, tc.target, record, ok)
		}
	}
	if _, ok := Instruction("testing.example.com", "", false); ok {
		t.Error("without a target there is no instruction")
	}
}

func TestAssessGuidesEachSituation(t *testing.T) {
	edge := netip.MustParseAddr("203.0.113.10")
	other := netip.MustParseAddr("198.51.100.7")
	for _, tc := range []struct {
		name             string
		target           string
		o                Observation
		connected        bool
		dns, cert, reach string
	}{
		{"no record yet", "203.0.113.10", Observation{Lookup: LookupNotFound, Reach: ReachNone}, false, StateBad, StateWait, StateWait},
		{"lookup failed", "203.0.113.10", Observation{Lookup: LookupFailed, Reach: ReachNone}, false, StateWait, StateWait, StateWait},
		{"connected directly", "203.0.113.10", Observation{Lookup: LookupFound, Addresses: []netip.Addr{edge}, Reach: ReachEdge, Status: 200}, true, StateOK, StateOK, StateOK},
		{"connected through a proxy", "203.0.113.10", Observation{Lookup: LookupFound, Addresses: []netip.Addr{other}, ViaProxy: true, Reach: ReachEdge}, true, StateOK, StateOK, StateOK},
		{"still at the old hosting", "203.0.113.10", Observation{Lookup: LookupFound, Addresses: []netip.Addr{other}, Reach: ReachOther, Status: 200}, false, StateBad, StateOK, StateBad},
		{"first certificate pending", "203.0.113.10", Observation{Lookup: LookupFound, Addresses: []netip.Addr{edge}, Reach: ReachCertificate}, false, StateOK, StateWait, StateWait},
		{"proxy refused by origin", "203.0.113.10", Observation{Lookup: LookupFound, Addresses: []netip.Addr{other}, ViaProxy: true, Reach: ReachProxyOrigin, Status: 526}, false, StateOK, StateWait, StateBad},
		{"right record, nothing answers", "203.0.113.10", Observation{Lookup: LookupFound, Addresses: []netip.Addr{edge}, Reach: ReachNone}, false, StateOK, StateWait, StateBad},
		{"no target, reaches this edge", "", Observation{Lookup: LookupFound, Addresses: []netip.Addr{other}, Reach: ReachEdge}, true, StateOK, StateOK, StateOK},
	} {
		a := Assess("testing.example.com", tc.target, true, tc.o)
		if a.Connected != tc.connected || a.DNS.State != tc.dns || a.Certificate.State != tc.cert || a.Reach.State != tc.reach {
			t.Errorf("%s: %+v", tc.name, a)
		}
		for _, step := range []Step{a.DNS, a.Certificate, a.Reach} {
			if step.Detail == "" {
				t.Errorf("%s: a step has no sentence", tc.name)
			}
		}
	}
	if a := Assess("local.test:8888", "", false, Observation{Lookup: LookupFound, Addresses: []netip.Addr{other}, Reach: ReachEdge}); a.Certificate.State != StateOK {
		t.Errorf("plain HTTP edge: certificate step %+v", a.Certificate)
	}
}
