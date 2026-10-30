package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/originauth"
	"github.com/ozal-project/ozal/internal/zonesetup"
)

// fakeZone is one Cloudflare zone held in memory.
type fakeZone struct {
	records  map[string][]certs.NameRecord
	ssl      string
	pullOn   bool
	certs    []certs.ZoneClientCertificate
	uploaded [][]byte
	fail     error
	calls    []string
}

func (f *fakeZone) RecordsAt(_ context.Context, name string) ([]certs.NameRecord, error) {
	return f.records[name], f.fail
}
func (f *fakeZone) CreateChallengeCNAME(_ context.Context, name, target string) error {
	f.calls = append(f.calls, "cname")
	if len(f.records[name]) > 0 {
		return certs.ErrNotEmpty
	}
	f.records[name] = []certs.NameRecord{{Type: "CNAME", Content: target}}
	return nil
}
func (f *fakeZone) ZoneSSLMode(context.Context, string) (string, error) { return f.ssl, f.fail }
func (f *fakeZone) SetZoneSSLStrict(context.Context, string) error {
	f.calls = append(f.calls, "strict")
	f.ssl = "strict"
	return nil
}
func (f *fakeZone) ZoneOriginPull(context.Context, string) (bool, []certs.ZoneClientCertificate, error) {
	return f.pullOn, f.certs, f.fail
}
func (f *fakeZone) UploadZoneClientCertificate(_ context.Context, _ string, certPEM, keyPEM []byte) (certs.ZoneClientCertificate, error) {
	f.calls = append(f.calls, "upload")
	f.uploaded = append(f.uploaded, keyPEM)
	c := certs.ZoneClientCertificate{ID: "c" + string(rune('0'+len(f.certs))), Certificate: string(certPEM), Status: "active", UploadedOn: "2026-10-30T22:00:0" + string(rune('0'+len(f.certs))) + "Z"}
	f.certs = append(f.certs, c)
	return c, nil
}
func (f *fakeZone) EnableZoneOriginPull(context.Context, string) error {
	f.calls = append(f.calls, "enable")
	f.pullOn = true
	return nil
}

func zoneConfig(t *testing.T, withProvider bool) config.Config {
	t.Helper()
	cfg := config.Config{
		TLS: config.TLS{Enabled: true, OriginClientCAFile: "/etc/ozal/origin-ca.pem", DNS01: &config.DNS01{
			DNSProvider: "builtin", Builtin: &config.DNS01Builtin{Zone: "acme.edge.net"},
		}},
		DNS: &config.DNS{Target: "203.0.113.10", Proxied: true},
	}
	if withProvider {
		token := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(token, []byte("test-token"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg.DNS.Cloudflare = &config.DNSCloudflare{Token: "file:" + token}
	}
	return cfg
}

func newService(t *testing.T, zone *fakeZone) (*Service, *originauth.Trust) {
	t.Helper()
	trust := originauth.NewTrust(x509.NewCertPool())
	return &Service{
		Cloudflare: func(token string) (Client, error) {
			if token != "test-token" {
				t.Errorf("token = %q", token)
			}
			return zone, nil
		},
		Identity: originauth.NewIdentity(t.TempDir(), trust),
		Trust:    trust,
		Now:      time.Now,
	}, trust
}

func states(r Report) map[zonesetup.StepName]zonesetup.State {
	out := map[zonesetup.StepName]zonesetup.State{}
	for _, s := range r.Steps {
		out[s.Name] = s.State
	}
	return out
}

// EDGE-009: a new zone behind Cloudflare goes from nothing to ready in one
// Apply, and the certificate the proxy now presents is one this edge trusts.
func TestApplySetsUpANewZone(t *testing.T) {
	zone := &fakeZone{records: map[string][]certs.NameRecord{}, ssl: "flexible"}
	svc, trust := newService(t, zone)
	cfg := zoneConfig(t, true)
	challenge := config.ChallengeFor(cfg.TLS, "example.dev")

	before := svc.Inspect(context.Background(), cfg, "example.dev")
	if before.Ready || before.Provider != "cloudflare" {
		t.Fatalf("before: %+v", before)
	}
	after, err := svc.Apply(context.Background(), cfg, "example.dev")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Ready {
		t.Fatalf("after Apply the zone is not ready: %+v", after.Steps)
	}
	if got := zone.records[challenge.Name]; len(got) != 1 || got[0].Content != challenge.Target {
		t.Fatalf("challenge record = %+v, want CNAME to %s", got, challenge.Target)
	}
	if len(zone.certs) != 1 || !zone.pullOn || zone.ssl != "strict" {
		t.Fatalf("zone after Apply: %+v", zone)
	}
	chain := originauth.ParseCertificates([]byte(zone.certs[0].Certificate))
	if !trust.Trusts(chain, time.Now()) {
		t.Fatal("the uploaded certificate is not trusted by this edge")
	}
	// A second Apply has nothing to do and uploads nothing more.
	zone.calls = nil
	if _, err := svc.Apply(context.Background(), cfg, "example.dev"); err != nil || len(zone.calls) != 0 {
		t.Fatalf("second Apply did %v (err %v)", zone.calls, err)
	}
}

// A zone-level certificate this edge does not trust is not mistaken for a
// working setup (it is why ozal.dev answered 520): ours is uploaded beside it.
func TestForeignProxyCertificateIsNotEnough(t *testing.T) {
	stranger := originauth.NewIdentity(t.TempDir(), nil)
	foreign, _, err := stranger.Ensure(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cfg := zoneConfig(t, true)
	challenge := config.ChallengeFor(cfg.TLS, "example.dev")
	zone := &fakeZone{
		records: map[string][]certs.NameRecord{challenge.Name: {{Type: "CNAME", Content: challenge.Target}}},
		ssl:     "strict", pullOn: true,
		certs: []certs.ZoneClientCertificate{{ID: "x", Certificate: string(foreign), Status: "active", UploadedOn: "2026-10-01T00:00:00Z"}},
	}
	svc, _ := newService(t, zone)
	if got := states(svc.Inspect(context.Background(), cfg, "example.dev"))[zonesetup.OriginPull]; got != zonesetup.Todo {
		t.Fatalf("origin pull with a foreign certificate = %s, want todo", got)
	}
	after, err := svc.Apply(context.Background(), cfg, "example.dev")
	if err != nil || !after.Ready {
		t.Fatalf("apply: %v %+v", err, after.Steps)
	}
	if len(zone.certs) != 2 || zone.certs[0].ID != "x" {
		t.Fatalf("the operator's certificate was touched: %+v", zone.certs)
	}
}

// A record at the challenge name that Ozal did not create is reported and
// left alone.
func TestApplyNeverReplacesAChallengeRecord(t *testing.T) {
	cfg := zoneConfig(t, true)
	challenge := config.ChallengeFor(cfg.TLS, "example.dev")
	zone := &fakeZone{records: map[string][]certs.NameRecord{challenge.Name: {{Type: "TXT", Content: "old"}}}, ssl: "strict"}
	svc, _ := newService(t, zone)
	report, _ := svc.Apply(context.Background(), cfg, "example.dev")
	if states(report)[zonesetup.Challenge] != zonesetup.Blocked {
		t.Fatalf("challenge = %+v", report.Steps)
	}
	for _, c := range zone.calls {
		if c == "cname" {
			t.Fatal("Apply tried to create a CNAME over an existing record")
		}
	}
}

// Without a provider the steps come from public DNS and manual instructions,
// and Apply refuses instead of pretending.
func TestWithoutProviderStepsAreManual(t *testing.T) {
	cfg := zoneConfig(t, false)
	challenge := config.ChallengeFor(cfg.TLS, "example.dev")
	svc, _ := newService(t, &fakeZone{})
	svc.LookupCNAME = func(_ context.Context, host string) (string, error) {
		if host != challenge.Name {
			t.Errorf("looked up %q", host)
		}
		return challenge.Target + ".", nil
	}
	report := svc.Inspect(context.Background(), cfg, "example.dev")
	got := states(report)
	if got[zonesetup.Challenge] != zonesetup.Done || got[zonesetup.Encryption] != zonesetup.Unknown || got[zonesetup.OriginPull] != zonesetup.Unknown {
		t.Fatalf("manual states = %v", got)
	}
	if _, err := svc.Apply(context.Background(), cfg, "example.dev"); err == nil {
		t.Fatal("Apply without a provider reported success")
	}
	svc.LookupCNAME = func(context.Context, string) (string, error) { return "", &net.DNSError{IsNotFound: true} }
	if got := states(svc.Inspect(context.Background(), cfg, "example.dev"))[zonesetup.Challenge]; got != zonesetup.Todo {
		t.Fatalf("missing record = %s, want todo", got)
	}
}

// A token without the needed permissions degrades to the manual view with
// the reason, and never leaks the token.
func TestProviderErrorFallsBackWithReason(t *testing.T) {
	cfg := zoneConfig(t, true)
	zone := &fakeZone{records: map[string][]certs.NameRecord{}, fail: errors.New("API error 403: code 10000: Authentication error")}
	svc, _ := newService(t, zone)
	svc.LookupCNAME = func(context.Context, string) (string, error) { return "", &net.DNSError{IsNotFound: true} }
	report := svc.Inspect(context.Background(), cfg, "example.dev")
	if report.Error == "" || states(report)[zonesetup.Encryption] != zonesetup.Unknown {
		t.Fatalf("report = %+v", report)
	}
	for _, s := range report.Steps {
		if s.Action != "" {
			t.Errorf("%s offers %q after the provider failed", s.Name, s.Action)
		}
	}
	if strings.Contains(report.Error, "test-token") {
		t.Fatal("the error repeats the token")
	}
}
