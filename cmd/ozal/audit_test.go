package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/audit"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
)

func TestRequiresDurableAccessAudit(t *testing.T) {
	auditConfig := &config.AuditConfig{Dir: "/var/lib/ozal/audit", Include: []string{"access"}}
	for _, tc := range []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{name: "audit disabled", cfg: config.Config{Routes: []config.Route{{AuthRequired: true}}}},
		{name: "admin-only audit does not require access family", cfg: config.Config{Audit: &config.AuditConfig{Dir: "/var/lib/ozal/audit", Include: []string{"admin"}}, Routes: []config.Route{{AuthRequired: true}}}},
		{name: "public defaults best effort", cfg: config.Config{Audit: auditConfig, Routes: []config.Route{{Host: "public.localhost", Target: "http://upstream.local"}}}},
		{name: "protected defaults required", cfg: config.Config{Audit: auditConfig, Routes: []config.Route{{Host: "private.localhost", Target: "http://upstream.local", AuthRequired: true}}}, want: true},
		{name: "public can opt into required", cfg: config.Config{Audit: auditConfig, Routes: []config.Route{{Host: "public.localhost", Target: "http://upstream.local", AuditMode: "required"}}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := requiresDurableAccessAudit(tc.cfg); got != tc.want {
				t.Fatalf("requiresDurableAccessAudit() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestRequiresDurableAdminAudit(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{name: "audit disabled", cfg: config.Config{}},
		{name: "admin family absent", cfg: config.Config{Audit: &config.AuditConfig{Dir: "/var/lib/ozal/audit", Include: []string{"access"}}}},
		{name: "admin defaults required", cfg: config.Config{Audit: &config.AuditConfig{Dir: "/var/lib/ozal/audit", Include: []string{"admin"}}}, want: true},
		{name: "best effort is optional", cfg: config.Config{Audit: &config.AuditConfig{Dir: "/var/lib/ozal/audit", Include: []string{"admin"}, AdminMode: "best_effort"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := requiresDurableAdminAudit(tc.cfg); got != tc.want {
				t.Fatalf("requiresDurableAdminAudit() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestAuditFailureReporterReturnsFailureAndLogsOnce(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	failure := errors.New("disk full")
	reporter := newAuditFailureReporter(nil, logger, failure)
	for range 2 {
		if err := reporter.Append(context.Background(), events.Event{Type: events.AccessAllowed}); !errors.Is(err, failure) {
			t.Fatalf("Append() error = %v, want %v", err, failure)
		}
	}
	if count := strings.Count(output.String(), "durable audit unavailable"); count != 1 {
		t.Fatalf("alert count = %d, want 1: %s", count, output.String())
	}
	if got := output.String(); strings.Contains(got, "disk full") || !strings.Contains(got, "failure_class=storage") {
		t.Fatalf("audit failure log must redact raw storage errors and preserve a stable class: %s", got)
	}
}

type auditAppendFunc func(context.Context, events.Event) error

func (f auditAppendFunc) Append(ctx context.Context, event events.Event) error {
	return f(ctx, event)
}

func TestAuditFailureReporterStatusTracksStorageFailuresOnly(t *testing.T) {
	t.Run("storage failure marks unavailable", func(t *testing.T) {
		failure := errors.New("disk full")
		reporter := newAuditFailureReporter(auditAppendFunc(func(context.Context, events.Event) error { return failure }), slog.Default(), nil)
		if status := reporter.Status(); !status.Available || status.FailureCount != 0 {
			t.Fatalf("initial status = %+v", status)
		}
		if err := reporter.Append(context.Background(), events.Event{Type: events.AccessAllowed}); !errors.Is(err, failure) {
			t.Fatalf("Append() error = %v, want %v", err, failure)
		}
		if status := reporter.Status(); status.Available || status.FailureCount != 1 || status.LastFailureAt == "" {
			t.Fatalf("failed status = %+v", status)
		}
	})
	t.Run("request cancellation does not mark storage unavailable", func(t *testing.T) {
		reporter := newAuditFailureReporter(auditAppendFunc(func(context.Context, events.Event) error { return context.Canceled }), slog.Default(), nil)
		if err := reporter.Append(context.Background(), events.Event{Type: events.AccessAllowed}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Append() error = %v, want cancellation", err)
		}
		if status := reporter.Status(); !status.Available || status.FailureCount != 0 || status.LastFailureAt != "" {
			t.Fatalf("canceled status = %+v", status)
		}
	})
	t.Run("successful append restores availability", func(t *testing.T) {
		calls := 0
		reporter := newAuditFailureReporter(auditAppendFunc(func(context.Context, events.Event) error {
			calls++
			if calls == 1 {
				return errors.New("record rejected")
			}
			return nil
		}), slog.Default(), nil)
		_ = reporter.Append(context.Background(), events.Event{Type: events.AccessAllowed})
		if status := reporter.Status(); status.Available {
			t.Fatalf("status after failed append = %+v", status)
		}
		if err := reporter.Append(context.Background(), events.Event{Type: events.AccessAllowed}); err != nil {
			t.Fatalf("recovered append: %v", err)
		}
		if status := reporter.Status(); !status.Available || status.FailureCount != 1 {
			t.Fatalf("status after recovery = %+v", status)
		}
	})
}

func TestResolveAuditPrivateKeyRejectsBroadOrSymlinkedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX private-file mode enforcement is not available on Windows")
	}
	root := t.TempDir()
	wide := filepath.Join(root, "wide.key")
	if err := os.WriteFile(wide, []byte("not-a-key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveAuditPrivateKey("file:" + wide); err == nil {
		t.Fatal("accepted a private key file accessible to group or other users")
	}
	private := filepath.Join(root, "private.key")
	if err := os.WriteFile(private, []byte("not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.key")
	if err := os.Symlink(private, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := resolveAuditPrivateKey("file:" + link); err == nil {
		t.Fatal("accepted a symlinked private key file")
	}
}

func TestAuditVerifyCLIReportsLocalOnlyGuaranteeAndTampering(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("audit private-file modes are POSIX-only until Windows ACL support is implemented")
	}
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := audit.Open(context.Background(), dir, audit.Options{})
	if err != nil {
		t.Fatalf("audit.Open: %v", err)
	}
	if err := writer.Append(context.Background(), events.Event{Type: events.AccessAllowed, Host: "app.localhost"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var output bytes.Buffer
	if err := runAudit(context.Background(), []string{"verify", "-dir", dir, "-json"}, &output); err != nil {
		t.Fatalf("runAudit verify: %v", err)
	}
	var verification audit.Verification
	if err := json.Unmarshal(output.Bytes(), &verification); err != nil {
		t.Fatalf("decode verification: %v", err)
	}
	if !verification.InternallyConsistent || verification.TamperEvident || verification.Records != 1 || verification.Note == "" {
		t.Fatalf("verification overstates guarantee or lost record: %+v", verification)
	}

	path := filepath.Join(dir, "audit-000001.jsonl")
	line, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if err := os.WriteFile(path, bytes.Replace(line, []byte("app.localhost"), []byte("evil.localhost"), 1), 0o600); err != nil {
		t.Fatalf("tamper record: %v", err)
	}
	if err := runAudit(context.Background(), []string{"verify", "-dir", dir}, &output); err == nil {
		t.Fatal("audit verify accepted a modified record")
	}
}

func TestAuditCLIKeygenCheckpointAndPinnedVerify(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("audit private-file modes are POSIX-only until Windows ACL support is implemented")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "audit")
	writer, err := audit.Open(context.Background(), dir, audit.Options{})
	if err != nil {
		t.Fatalf("audit.Open: %v", err)
	}
	if err := writer.Append(context.Background(), events.Event{Type: events.AccessAllowed, Host: "app.localhost"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	privatePath := filepath.Join(root, "audit.key")
	publicPath := filepath.Join(root, "audit.pub")
	checkpointPath := filepath.Join(root, "checkpoint.json")
	var output bytes.Buffer
	if err := runAudit(context.Background(), []string{"keygen", "-private-out", privatePath, "-public-out", publicPath}, &output); err != nil {
		t.Fatalf("audit keygen: %v", err)
	}
	privateData, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}
	publicData, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}
	if publicBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(publicData))); err != nil || len(publicBytes) != 32 {
		t.Fatalf("public key encoding length=%d err=%v", len(publicBytes), err)
	}
	privateValue := strings.TrimSpace(string(privateData))
	if strings.Contains(output.String(), privateValue) {
		t.Fatal("audit keygen printed the private key")
	}
	privateBytes, err := base64.StdEncoding.DecodeString(privateValue)
	if err != nil || len(privateBytes) != 64 {
		t.Fatalf("private key encoding length=%d err=%v", len(privateBytes), err)
	}
	privateInfo, err := os.Stat(privatePath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if got := privateInfo.Mode().Perm(); got&0o077 != 0 || got&0o400 == 0 {
		t.Fatalf("private key mode = %04o, want owner-only readable", got)
	}
	output.Reset()
	if err := runAudit(context.Background(), []string{"checkpoint", "-dir", dir, "-key", "file:" + privatePath, "-out", checkpointPath}, &output); err != nil {
		t.Fatalf("audit checkpoint: %v", err)
	}
	output.Reset()
	if err := runAudit(context.Background(), []string{"verify", "-dir", dir, "-checkpoint", checkpointPath, "-public-key", publicPath, "-json"}, &output); err != nil {
		t.Fatalf("audit verify with pinned key: %v", err)
	}
	var verified audit.Verification
	if err := json.Unmarshal(output.Bytes(), &verified); err != nil {
		t.Fatalf("decode verification: %v", err)
	}
	if verified.TamperEvident || !verified.CheckpointVerified || verified.CheckpointSeq != 1 || verified.Records != 1 {
		t.Fatalf("verification = %+v", verified)
	}
	if err := runAudit(context.Background(), []string{"keygen", "-private-out", privatePath, "-public-out", publicPath}, &output); err == nil {
		t.Fatal("audit keygen overwrote existing key files")
	}
}

func TestAuditRepairIsExplicitAndRecordsTailTruncation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("audit private-file modes are POSIX-only until Windows ACL support is implemented")
	}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := audit.Open(ctx, dir, audit.Options{})
	if err != nil {
		t.Fatalf("audit.Open: %v", err)
	}
	if err := writer.Append(ctx, events.Event{Type: events.AccessAllowed, Host: "app.localhost"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	path := filepath.Join(dir, "audit-000001.jsonl")
	partial := []byte(`{"v":1,"partial":`)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	if _, err := file.Write(partial); err != nil {
		t.Fatalf("append partial tail: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close partial segment: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var preview bytes.Buffer
	if err := runAudit(ctx, []string{"repair", "-dir", dir}, &preview); err != nil {
		t.Fatalf("repair preview: %v", err)
	}
	if !strings.Contains(preview.String(), "discarded_bytes=") {
		t.Fatalf("repair preview omitted the planned data loss: %s", preview.String())
	}
	afterPreview, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(afterPreview, before) {
		t.Fatalf("repair preview changed the segment: err=%v", err)
	}

	var applied bytes.Buffer
	if err := runAudit(ctx, []string{"repair", "-dir", dir, "-apply"}, &applied); err != nil {
		t.Fatalf("apply repair: %v", err)
	}
	var records []audit.Record
	verification, err := audit.ReadVerified(ctx, dir, func(record audit.Record) error {
		records = append(records, record)
		return nil
	})
	if err != nil || !verification.InternallyConsistent || len(records) != 2 {
		t.Fatalf("repaired chain: verification=%+v records=%+v err=%v", verification, records, err)
	}
	marker := records[1]
	if string(marker.Type) != "audit.repaired" || marker.Code != "partial_tail_truncated" || marker.Count != 0 ||
		!strings.Contains(marker.Reason, "discarded_bytes="+strconv.Itoa(len(partial))) {
		t.Fatalf("repair marker = %+v", marker)
	}
}

func TestAuditKeygenStopsWhenContextIsCanceledBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("audit private-file modes are POSIX-only until Windows ACL support is implemented")
	}
	root := t.TempDir()
	privatePath := filepath.Join(root, "audit.key")
	publicPath := filepath.Join(root, "audit.pub")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runAudit(ctx, []string{"keygen", "-private-out", privatePath, "-public-out", publicPath}, &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runAudit returned %v, want context.Canceled", err)
	}
	for _, path := range []string{privatePath, publicPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("key file %q exists or could not be checked: %v", path, err)
		}
	}
}
