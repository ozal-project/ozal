package zonesetup

import "testing"

func base() Observed {
	return Observed{
		Zone: "example.dev", Provider: true, Proxied: true,
		ChallengeDelegated: true, ChallengeName: "_acme-challenge.example.dev", ChallengeTarget: "example-dev.acme.edge.net",
		ChallengeRead: true, ChallengeCurrent: "example-dev.acme.edge.net",
		SSLMode: "strict", OriginAuth: true, PullEnabled: true, PullTrusted: true,
	}
}

func step(t *testing.T, o Observed, name StepName) Step {
	t.Helper()
	for _, s := range Plan(o) {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no %s step", name)
	return Step{}
}

func TestReadyZone(t *testing.T) {
	if steps := Plan(base()); !Ready(steps) {
		t.Fatalf("a complete zone is not ready: %+v", steps)
	}
}

func TestChallenge(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Observed)
		state  State
		action Action
	}{
		{"missing with provider", func(o *Observed) { o.ChallengeCurrent = "" }, Todo, CreateCNAME},
		{"missing by hand", func(o *Observed) { o.ChallengeCurrent, o.Provider = "", false }, Todo, ""},
		// Never replace a record someone else created.
		{"points elsewhere", func(o *Observed) { o.ChallengeCurrent = "other.example.net" }, Blocked, ""},
		{"lookup failed", func(o *Observed) { o.ChallengeRead, o.ChallengeCurrent = false, "" }, Unknown, ""},
		{"provider writes it", func(o *Observed) { o.ChallengeDelegated = false }, NotNeeded, ""},
	}
	for _, c := range cases {
		o := base()
		c.change(&o)
		s := step(t, o, Challenge)
		if s.State != c.state || s.Action != c.action {
			t.Errorf("%s: got %s/%q, want %s/%q", c.name, s.State, s.Action, c.state, c.action)
		}
		if (s.State == Todo || s.State == Blocked) && s.Manual == "" {
			t.Errorf("%s: no manual instruction", c.name)
		}
	}
}

func TestEncryption(t *testing.T) {
	cases := map[string]struct {
		mode     string
		provider bool
		proxied  bool
		state    State
		action   Action
	}{
		"strict":        {"strict", true, true, Done, ""},
		"full is kept":  {"full", true, true, Done, ""},
		"flexible":      {"flexible", true, true, Todo, SetStrict},
		"off":           {"off", true, true, Todo, SetStrict},
		"unknown value": {"weird", true, true, Unknown, ""},
		"no provider":   {"", false, true, Unknown, ""},
		"not proxied":   {"flexible", true, false, NotNeeded, ""},
	}
	for name, c := range cases {
		o := base()
		o.SSLMode, o.Provider, o.Proxied = c.mode, c.provider, c.proxied
		if s := step(t, o, Encryption); s.State != c.state || s.Action != c.action {
			t.Errorf("%s: got %s/%q, want %s/%q", name, s.State, s.Action, c.state, c.action)
		}
	}
}

func TestOriginPull(t *testing.T) {
	cases := map[string]struct {
		change func(*Observed)
		state  State
		action Action
	}{
		"trusted and on":  {func(o *Observed) {}, Done, ""},
		"trusted but off": {func(o *Observed) { o.PullEnabled = false }, Todo, EnablePull},
		// An enabled certificate this edge does not trust still yields 520.
		"on with a foreign cert": {func(o *Observed) { o.PullTrusted = false }, Todo, UploadIdentity},
		"nothing":                {func(o *Observed) { o.PullTrusted, o.PullEnabled = false, false }, Todo, UploadIdentity},
		"edge does not check":    {func(o *Observed) { o.OriginAuth, o.PullTrusted = false, false }, NotNeeded, ""},
		"no provider":            {func(o *Observed) { o.Provider = false }, Unknown, ""},
		"checks but not proxied": {func(o *Observed) { o.Proxied = false }, Blocked, ""},
	}
	for name, c := range cases {
		o := base()
		c.change(&o)
		if s := step(t, o, OriginPull); s.State != c.state || s.Action != c.action {
			t.Errorf("%s: got %s/%q, want %s/%q", name, s.State, s.Action, c.state, c.action)
		}
	}
}

// Without a provider nothing can be read or done by the edge: every proxy
// step says how to do it by hand and none claims to be done.
func TestWithoutProviderEveryStepIsManual(t *testing.T) {
	o := base()
	o.Provider, o.ChallengeCurrent = false, ""
	for _, s := range Plan(o) {
		if s.Action != "" {
			t.Errorf("%s offers action %q without a provider", s.Name, s.Action)
		}
		if s.State == Done || s.Manual == "" {
			t.Errorf("%s: state %s, manual %q", s.Name, s.State, s.Manual)
		}
	}
	if Ready(Plan(o)) {
		t.Fatal("a zone without a provider claims to be ready")
	}
}
