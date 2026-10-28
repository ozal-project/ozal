package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/audit"
	"github.com/switchboard-project/switchboard/internal/events"
)

func TestPolicyWhoCanStopsWhenContextIsCanceledBeforeConfigRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := runPolicy(ctx, []string{"who-can", "-host", "app.example.com", "-config", filepath.Join(t.TempDir(), "missing.json")}, &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runPolicy returned %v, want context.Canceled", err)
	}
}

func TestPolicyWhoCanDescribesConfiguredReachabilityWithoutSecrets(t *testing.T) {
	configPath := writeValidateConfig(t, policyTestConfig)
	for _, tc := range []struct {
		host          string
		wantReachable string
		wantAuth      bool
		wantSemantic  string
	}{
		{host: "www.example.com", wantReachable: "any visitor who passes the transport layer", wantAuth: false, wantSemantic: "no Switchboard login requirement"},
		{host: "APP.example.com.", wantReachable: "an authenticated principal whose verified email matches an allowed email or domain and who carries every required group", wantAuth: true, wantSemantic: "every required_groups entry"},
		{host: "legacy.example.com", wantReachable: "any authenticated principal", wantAuth: true, wantSemantic: "legacy auth_required"},
	} {
		var out bytes.Buffer
		if err := runPolicy(context.Background(), []string{"who-can", "-config", configPath, "-host", tc.host, "-json"}, &out); err != nil {
			t.Fatalf("who-can %s: %v\n%s", tc.host, err, out.String())
		}
		var report policyWhoCanReport
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatalf("decode report: %v\n%s", err, out.String())
		}
		if report.Host == "" || report.ReachableBy != tc.wantReachable || report.RequiresAuth != tc.wantAuth {
			t.Fatalf("who-can report = %+v", report)
		}
		if !strings.Contains(strings.Join(report.Semantics, " "), tc.wantSemantic) {
			t.Fatalf("semantics %v do not include %q", report.Semantics, tc.wantSemantic)
		}
		if strings.Contains(out.String(), policyTestSecret) || strings.Contains(out.String(), "client_secret") {
			t.Fatalf("policy report leaked auth config: %s", out.String())
		}
	}
}

func TestPolicyReviewKeepsCandidateUseUnknownAndReportsOpaqueSubjects(t *testing.T) {
	ctx := context.Background()
	auditDir := filepath.Join(t.TempDir(), "audit")
	writer, err := audit.Open(ctx, auditDir, audit.Options{})
	if err != nil {
		t.Skipf("audit storage is unsupported here: %v", err)
	}
	start := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, event := range []events.Event{
		{Time: start, Type: events.AccessAllowed, Actor: "passkey:random-user-id", Host: "ops.example.com"},
		{Time: start.Add(time.Minute), Type: events.AccessDenied, Actor: "passkey:random-user-id", Host: "ops.example.com"},
		{Time: start.Add(2 * time.Minute), Type: events.AccessStepUp, Actor: "oidc:opaque-subject", Host: "ops.example.com"},
		{Time: start.Add(2 * time.Minute), Type: events.AccessAllowed, Actor: "another-subject", Host: "app.example.com"},
		{Time: start.Add(-time.Second), Type: events.AccessAllowed, Actor: "outside-window", Host: "ops.example.com"},
	} {
		if err := writer.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := writeValidateConfig(t, policyTestConfig)
	var out bytes.Buffer
	args := []string{"review", "-config", configPath, "-dir", auditDir, "-host", "OPS.example.com.", "-since", start.Format(time.RFC3339), "-until", start.Add(3 * time.Minute).Format(time.RFC3339), "-subjects", "1", "-json"}
	if err := runPolicy(ctx, args, &out); err != nil {
		t.Fatalf("policy review: %v\n%s", err, out.String())
	}
	var report policyReviewReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode review: %v\n%s", err, out.String())
	}
	if report.Host != "ops.example.com" || report.DecisionEvents != 3 || report.Allowed != 1 || report.Denied != 1 || report.StepUp != 1 ||
		!report.Audit.InternallyConsistent || report.Audit.TamperEvident || len(report.Candidates) != 1 || len(report.ObservedSubjects) != 1 || report.Unattributed != 1 {
		t.Fatalf("review summary = %+v", report)
	}
	candidate := report.Candidates[0]
	if candidate.Kind != "email" || candidate.Value != "Ana@Example.com" || candidate.Usage != "unknown" || !strings.Contains(candidate.Reason, "opaque subject") {
		t.Fatalf("candidate assessment = %+v", candidate)
	}
	for _, observed := range report.ObservedSubjects {
		if strings.Contains(out.String(), "passkey:random-user-id") || strings.Contains(out.String(), "oidc:opaque-subject") || !strings.HasPrefix(observed.Fingerprint, "subject-fingerprint:") {
			t.Fatalf("raw audit subject leaked: %+v\n%s", observed, out.String())
		}
	}
	if !strings.Contains(report.HistoryAssessment, "no candidate is classified as unused") {
		t.Fatalf("history assessment = %q", report.HistoryAssessment)
	}
}

func TestPolicyReviewRetainsOnlyTheConfiguredSubjectBound(t *testing.T) {
	report := policyReviewReport{SubjectLimit: 1}
	counts := make(map[string]policyReviewSubjectCounts)
	for _, record := range []audit.Record{
		{Type: events.AccessAllowed, Actor: "first"},
		{Type: events.AccessDenied, Actor: "first"},
		{Type: events.AccessStepUp, Actor: "second"},
		{Type: events.AccessAllowed},
	} {
		recordPolicyReviewDecision(&report, counts, record)
	}
	if report.DecisionEvents != 4 || report.Allowed != 2 || report.Denied != 1 || report.StepUp != 1 ||
		report.MissingActor != 1 || report.Unattributed != 1 || len(counts) != 1 || counts["first"].allowed != 1 || counts["first"].denied != 1 {
		t.Fatalf("bounded review aggregation = report:%+v counts:%+v", report, counts)
	}
}

func TestPolicyReviewRequiresAnExplicitValidWindowAndVerifiedAudit(t *testing.T) {
	configPath := writeValidateConfig(t, policyTestConfig)
	for _, args := range [][]string{
		{"review", "-config", configPath, "-dir", filepath.Join(t.TempDir(), "audit"), "-host", "ops.example.com"},
		{"review", "-config", configPath, "-dir", filepath.Join(t.TempDir(), "audit"), "-host", "ops.example.com", "-since", "yesterday"},
		{"review", "-config", configPath, "-dir", filepath.Join(t.TempDir(), "audit"), "-host", "ops.example.com", "-since", "2026-10-25T12:00:00Z", "-until", "2026-10-25T11:00:00Z"},
		{"review", "-config", configPath, "-dir", filepath.Join(t.TempDir(), "audit"), "-host", "www.example.com", "-since", "2026-10-25T00:00:00Z"},
	} {
		if err := runPolicy(context.Background(), args, &bytes.Buffer{}); err == nil {
			t.Errorf("runPolicy(%v) unexpectedly succeeded", args)
		}
	}

	auditDir := filepath.Join(t.TempDir(), "audit")
	writer, err := audit.Open(context.Background(), auditDir, audit.Options{})
	if err != nil {
		t.Skipf("audit storage is unsupported here: %v", err)
	}
	if err := writer.Append(context.Background(), events.Event{Type: events.AccessAllowed, Actor: "private-subject", Host: "ops.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	segment := filepath.Join(auditDir, "audit-000001.jsonl")
	data, err := os.ReadFile(segment)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("ops.example.com"), []byte("bad.example.com"), 1)
	if err := os.WriteFile(segment, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = runPolicy(context.Background(), []string{"review", "-config", configPath, "-dir", auditDir, "-host", "ops.example.com", "-since", "2026-10-25T00:00:00Z"}, &out)
	if err == nil || out.Len() != 0 {
		t.Fatalf("tampered audit should fail before printing a report: err=%v output=%q", err, out.String())
	}
}
