package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/audit"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/policycontext"
)

func TestPolicyReplayRunsShadowRuleOnlyOnMatchingVerifiedEvents(t *testing.T) {
	ctx := context.Background()
	auditDir := filepath.Join(t.TempDir(), "audit")
	writer, err := audit.Open(ctx, auditDir, audit.Options{})
	if err != nil {
		t.Skipf("audit storage is unsupported here: %v", err)
	}
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	known := policycontext.Build(policycontext.Input{
		ObservedAt:      at,
		SessionMethod:   policycontext.Known("passkey", policycontext.SourceSession),
		Sensitivity:     policycontext.Known("restricted", policycontext.SourceRouteConfig),
		DeviceFirstSeen: policycontext.Known(false, policycontext.SourceSession),
	})
	oidc := policycontext.Build(policycontext.Input{
		ObservedAt:      at,
		SessionMethod:   policycontext.Known("oidc", policycontext.SourceSession),
		Sensitivity:     policycontext.Known("restricted", policycontext.SourceRouteConfig),
		DeviceFirstSeen: policycontext.Unknown[bool](policycontext.SourceNotRecorded),
	})
	unknown := policycontext.Build(policycontext.Input{
		ObservedAt:      at,
		SessionMethod:   policycontext.Unknown[string](policycontext.SourceNotRecorded),
		Sensitivity:     policycontext.Known("restricted", policycontext.SourceRouteConfig),
		DeviceFirstSeen: policycontext.Unknown[bool](policycontext.SourceNotRecorded),
	})
	records := []events.Event{
		{Time: at, Type: events.AccessAllowed, Actor: "sb:known-user", Host: "app.example.com", Context: &known},
		{Time: at.Add(time.Second), Type: events.AccessDenied, Actor: "oidc:step-up-user", Host: "app.example.com", Context: &oidc},
		{Time: at.Add(2 * time.Second), Type: events.AccessAllowed, Actor: "oidc:unknown-user", Host: "app.example.com", Context: &unknown},
		{Time: at.Add(3 * time.Second), Type: events.AccessAllowed, Actor: "sb:legacy-user", Host: "app.example.com"},
		{Time: at.Add(time.Second), Type: events.AccessDenied, Actor: "sb:other-route", Host: "other.example.com", Context: &oidc},
	}
	for _, event := range records {
		if err := writer.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	configPath := writeValidateConfig(t, contextPolicyTestConfig)
	var out bytes.Buffer
	err = runPolicy(context.Background(), []string{"replay", "-config", configPath, "-dir", auditDir, "-host", "APP.example.com.", "-json"}, &out)
	if err != nil {
		t.Fatalf("policy replay: %v\n%s", err, out.String())
	}
	var report policyReplayReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if report.Host != "app.example.com" || report.AccessEvents != 4 || report.Evaluated != 3 || report.MissingContext != 1 ||
		report.WouldAllow != 1 || report.WouldStepUp != 1 || report.Unknown != 2 || report.WouldDeny != 0 {
		t.Fatalf("replay summary = %+v", report)
	}
	if !report.Audit.InternallyConsistent || report.Audit.TamperEvident || len(report.Examples) != 3 {
		t.Fatalf("replay claims or examples = %+v", report)
	}
	if report.Examples[0].Actor != "oidc:step-up-user" || report.Examples[0].Would != "step_up" ||
		report.Examples[1].Actor != "oidc:unknown-user" || len(report.Examples[1].UnknownSignals) == 0 ||
		report.Examples[2].Actor != "sb:legacy-user" || report.Examples[2].UnknownSignals[0] != "context (source: not_recorded)" {
		t.Fatalf("non-allow examples = %+v", report.Examples)
	}

	var window bytes.Buffer
	if err := runPolicy(context.Background(), []string{
		"replay", "-config", configPath, "-dir", auditDir, "-host", "app.example.com",
		"-since", at.Add(time.Second).Format(time.RFC3339), "-until", at.Add(3 * time.Second).Format(time.RFC3339), "-examples", "1",
	}, &window); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(window.String(), "events=2 evaluated=2") || !strings.Contains(window.String(), "examples omitted: 1") {
		t.Fatalf("bounded replay window = %s", window.String())
	}
}

func TestPolicyReplayRejectsInvalidWindowAndMissingRule(t *testing.T) {
	configPath := writeValidateConfig(t, contextPolicyTestConfig)
	for _, args := range [][]string{
		{"replay", "-config", configPath, "-host", "app.example.com", "-since", "yesterday"},
		{"replay", "-config", configPath, "-host", "app.example.com", "-since", "2026-10-25T12:00:00Z", "-until", "2026-10-25T11:00:00Z"},
		{"replay", "-config", configPath, "-host", "www.example.com", "-dir", filepath.Join(t.TempDir(), "audit")},
	} {
		if err := runPolicy(context.Background(), args, &bytes.Buffer{}); err == nil {
			t.Fatalf("runPolicy(%v) unexpectedly succeeded", args)
		}
	}
}
