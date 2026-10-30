package domaincheck

import "testing"

func TestAssessChallenge(t *testing.T) {
	const name, target = "_acme-challenge.example.org", "example.org.acme.ozal.dev"
	for _, c := range []struct {
		label       string
		delegated   bool
		canonical   string
		failed      bool
		state       string
		wantsRecord bool
	}{
		{"provider writes it", false, "", false, StateOK, false},
		{"in place", true, target + ".", false, StateOK, false},
		{"in place, upper case", true, "Example.ORG.acme.ozal.dev.", false, StateOK, false},
		{"missing", true, name + ".", false, StateBad, true},
		{"lookup failed", true, "", true, StateBad, true},
		{"points elsewhere", true, "someone.else.net.", false, StateBad, true},
	} {
		got := AssessChallenge(name, target, c.delegated, c.canonical, c.failed)
		if got.State != c.state || (got.Record != nil) != c.wantsRecord {
			t.Errorf("%s: %+v", c.label, got)
		}
		if got.Record != nil && (got.Record.Type != "CNAME" || got.Record.Name != name || got.Record.Content != target) {
			t.Errorf("%s: record %+v", c.label, got.Record)
		}
	}
}
