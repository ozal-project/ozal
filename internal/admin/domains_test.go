package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/domaincheck"
	"github.com/ozal-project/ozal/internal/domaincheck/probe"
	"github.com/ozal-project/ozal/internal/events"
)

// applicationManager accepts saved applications like the route manager does.
type applicationManager struct {
	*fakeManager
	saved []config.Route
}

func (m *applicationManager) PutApplication(route config.Route, _ string) error {
	m.saved = append(m.saved, route)
	return nil
}

func tlsConfig(dns01 bool) config.Config {
	cfg := config.Config{Listen: ":443", Admin: config.Admin{Token: "secret"},
		TLS:    config.TLS{Enabled: true, Domains: []string{"edge.example.com"}, CacheDir: "/var/cache/sb"},
		Routes: []config.Route{{Host: "edge.example.com", Target: "http://127.0.0.1:3000"}}}
	if dns01 {
		cfg.TLS.Domains = []string{"*.example.com"}
		cfg.TLS.Email = "ops@example.com"
		cfg.TLS.DNS01 = &config.DNS01{StateDir: "/var/lib/sb", DirectoryURL: "https://acme.test/dir", DNSToken: "file:/run/token"}
	}
	return cfg
}

// With HTTP-01 a hostname no certificate lists is saved: the listener
// orders its certificate on the first visit (routeHostPolicy). Before
// UI-006 every new domain was refused until tls.domains was edited by hand.
func TestSaveAcceptsANewPublicDomainWhenCertificatesAreAutomatic(t *testing.T) {
	manager := &applicationManager{fakeManager: &fakeManager{cfg: tlsConfig(false)}}
	handler := New(manager, nil)
	body := `{"expected":"` + manager.ConfigDigest() + `","access_mode":"public","route":{"host":"domain1.example.org","target":"http://127.0.0.1:4000"}}`
	rec := request(handler, http.MethodPost, "/_/api/apps/plan", "secret", body)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"tls":"automatic_on_first_visit"`) {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodPost, "/_/api/apps", "secret", body); rec.Code != 200 || len(manager.saved) != 1 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	// Names a public CA refuses stay uncovered.
	local := strings.Replace(body, "domain1.example.org", "intranet.localhost", 1)
	if rec := request(handler, http.MethodPost, "/_/api/apps", "secret", local); rec.Code != 422 {
		t.Fatalf("a .localhost name was accepted for public TLS: %d", rec.Code)
	}
}

// DNS-01 serves one managed certificate: hostnames outside its names are
// still refused.
func TestSaveRefusesNamesOutsideTheManagedCertificate(t *testing.T) {
	manager := &applicationManager{fakeManager: &fakeManager{cfg: tlsConfig(true)}}
	handler := New(manager, nil)
	for host, want := range map[string]int{"testing.example.com": 200, "domain1.example.org": 422, "a.b.example.com": 422} {
		body := `{"expected":"` + manager.ConfigDigest() + `","access_mode":"public","route":{"host":"` + host + `","target":"http://127.0.0.1:4000"}}`
		if rec := request(handler, http.MethodPost, "/_/api/apps", "secret", body); rec.Code != want {
			t.Errorf("save %s: %d, want %d (%s)", host, rec.Code, want, rec.Body)
		}
	}
}

type fakeDomains struct {
	checked []string
	created []string
	err     error
}

func (f *fakeDomains) Check(_ context.Context, _ config.Config, host string) probe.Report {
	f.checked = append(f.checked, host)
	return probe.Report{Host: host, Assessment: domaincheck.Assessment{Connected: true}}
}

func (f *fakeDomains) CreateRecord(_ context.Context, _ config.Config, host string) (domaincheck.Record, error) {
	if f.err != nil {
		return domaincheck.Record{}, f.err
	}
	f.created = append(f.created, host)
	return domaincheck.Record{Type: "A", Name: host, Content: "203.0.113.10"}, nil
}

func TestConnectionEndpointsOnlyTouchConfiguredHostnames(t *testing.T) {
	cfg := tlsConfig(false)
	manager := &fakeManager{cfg: cfg}
	domains := &fakeDomains{}
	sink := &events.Recorder{}
	handler := New(manager, sink, WithDomains(domains))

	if rec := request(handler, http.MethodPost, "/_/api/apps/edge.example.com/check", "", ""); rec.Code != 401 {
		t.Fatalf("anonymous check: %d", rec.Code)
	}
	if rec := request(handler, http.MethodPost, "/_/api/apps/unknown.example.net/check", "secret", ""); rec.Code != 404 || len(domains.checked) != 0 {
		t.Fatalf("an unconfigured hostname was probed: %d", rec.Code)
	}
	if rec := request(handler, http.MethodPost, "/_/api/apps/edge.example.com/check", "secret", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"connected":true`) {
		t.Fatalf("check: %d %s", rec.Code, rec.Body)
	}

	// No provider: the operator adds the record by hand.
	if rec := request(handler, http.MethodPost, "/_/api/apps/edge.example.com/dns", "secret", ""); rec.Code != 409 || len(domains.created) != 0 {
		t.Fatalf("record without a provider: %d", rec.Code)
	}
	cfg.DNS = &config.DNS{Target: "203.0.113.10", Cloudflare: &config.DNSCloudflare{Token: "file:/run/cf"}}
	manager.cfg = cfg
	if rec := request(handler, http.MethodPost, "/_/api/apps/unknown.example.net/dns", "secret", ""); rec.Code != 404 {
		t.Fatalf("record for an unconfigured hostname: %d", rec.Code)
	}
	domains.err = certs.ErrRecordExists
	if rec := request(handler, http.MethodPost, "/_/api/apps/edge.example.com/dns", "secret", ""); rec.Code != 409 {
		t.Fatalf("existing record: %d", rec.Code)
	}
	domains.err = errors.Join(certs.ErrAmbiguous, errors.New("timeout"))
	if rec := request(handler, http.MethodPost, "/_/api/apps/edge.example.com/dns", "secret", ""); rec.Code != 502 || !strings.Contains(rec.Body.String(), "may exist") {
		t.Fatalf("ambiguous create: %d %s", rec.Code, rec.Body)
	}
	domains.err = nil
	if rec := request(handler, http.MethodPost, "/_/api/apps/edge.example.com/dns", "secret", ""); rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if !sink.Has(events.DNSRecordCreated) {
		t.Fatal("a created record emitted no event")
	}
}
