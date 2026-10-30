package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func checkConfig() *config.Config {
	return &config.Config{TLS: config.TLS{
		Enabled: true,
		DNS01: &config.DNS01{
			DNSProvider: "builtin",
			Builtin: &config.DNS01Builtin{
				Zone: "acme.example.net", Listen: "203.0.113.7:53", Nameservers: []string{"ns1.example.net"},
			},
			ChallengeDelegation: map[string]string{
				"a.example": "a-example.acme.example.net",
				"b.example": "b-example.acme.example.net",
			},
		},
	}}
}

func healthyEnv() checkEnv {
	return checkEnv{
		LookupCNAME: func(_ context.Context, host string) (string, error) {
			return strings.NewReplacer("_acme-challenge.a.example", "a-example.acme.example.net.",
				"_acme-challenge.b.example", "b-example.acme.example.net.").Replace(host), nil
		},
		LookupHost: func(context.Context, string) ([]string, error) { return []string{"203.0.113.7"}, nil },
		Bind:       func(string, string) error { return nil },
	}
}

func levels(findings []checkFinding, level checkLevel) []string {
	var out []string
	for _, f := range findings {
		if f.Level == level {
			out = append(out, f.Text)
		}
	}
	return out
}

func TestCheckPassesWhenEverythingIsInPlace(t *testing.T) {
	findings := checkDNS01(context.Background(), checkConfig(), healthyEnv())
	if fails := levels(findings, checkFail); len(fails) != 0 {
		t.Fatalf("unexpected failures: %v", fails)
	}
	if warns := levels(findings, checkWarn); len(warns) != 1 || !strings.Contains(warns[0], "from the Internet") {
		t.Fatalf("want exactly the reachability warning, got %v", warns)
	}
}

func TestCheckNamesTheMissingCNAME(t *testing.T) {
	env := healthyEnv()
	env.LookupCNAME = func(_ context.Context, host string) (string, error) {
		if host == "_acme-challenge.b.example" {
			return host + ".", nil // no CNAME: the resolver answers with the name itself
		}
		return "a-example.acme.example.net.", nil
	}
	fails := levels(checkDNS01(context.Background(), checkConfig(), env), checkFail)
	if len(fails) != 1 || !strings.Contains(fails[0], "_acme-challenge.b.example CNAME b-example.acme.example.net.") {
		t.Fatalf("want the exact record to create, got %v", fails)
	}
}

func TestCheckRejectsAWrongCNAMETarget(t *testing.T) {
	env := healthyEnv()
	env.LookupCNAME = func(context.Context, string) (string, error) { return "other.example.org.", nil }
	if fails := levels(checkDNS01(context.Background(), checkConfig(), env), checkFail); len(fails) != 2 {
		t.Fatalf("want one failure per domain, got %v", fails)
	}
}

func TestCheckCatchesMissingAndMismatchedGlue(t *testing.T) {
	env := healthyEnv()
	env.LookupHost = func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	if fails := levels(checkDNS01(context.Background(), checkConfig(), env), checkFail); len(fails) != 1 || !strings.Contains(fails[0], "glue") {
		t.Fatalf("want a glue failure, got %v", fails)
	}
	env.LookupHost = func(context.Context, string) ([]string, error) { return []string{"198.51.100.1"}, nil }
	if fails := levels(checkDNS01(context.Background(), checkConfig(), env), checkFail); len(fails) != 1 || !strings.Contains(fails[0], "198.51.100.1") {
		t.Fatalf("want a mismatch failure, got %v", fails)
	}
}

func TestCheckTreatsAnOccupiedPortAsAWarning(t *testing.T) {
	env := healthyEnv()
	env.Bind = func(string, string) error { return errors.New("address already in use") }
	findings := checkDNS01(context.Background(), checkConfig(), env)
	if fails := levels(findings, checkFail); len(fails) != 0 {
		t.Fatalf("a busy port must not fail the check (the edge may be running): %v", fails)
	}
	if warns := levels(findings, checkWarn); len(warns) != 3 {
		t.Fatalf("want udp, tcp and reachability warnings, got %v", warns)
	}
}

func TestCheckRefusesWithoutDNS01(t *testing.T) {
	findings := checkDNS01(context.Background(), &config.Config{}, healthyEnv())
	if len(findings) != 1 || findings[0].Level != checkFail {
		t.Fatalf("got %v", findings)
	}
}

func TestReportExitsNonZeroOnlyOnFailure(t *testing.T) {
	var out bytes.Buffer
	if err := reportDNS01Check(&out, []checkFinding{{checkOK, "x"}, {checkWarn, "y"}}); err != nil {
		t.Fatalf("warnings alone must pass: %v", err)
	}
	if err := reportDNS01Check(&out, []checkFinding{{checkFail, "z"}}); err == nil {
		t.Fatal("a failure must return an error")
	}
}
