package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/domaincheck"
	"github.com/ozal-project/ozal/internal/routemanager"
)

// zoneManager applies zone edits to its in-memory configuration with the
// same digest rule as the route manager.
type zoneManager struct {
	*fakeManager
	digest string
}

func (m *zoneManager) ConfigDigest() string { return m.digest }

func (m *zoneManager) AddZone(name, expected string) error {
	return m.change(expected, func(cfg config.Config) (config.Config, error) { return config.WithZone(cfg, name) })
}

func (m *zoneManager) RemoveZone(name, expected string) error {
	return m.change(expected, func(cfg config.Config) (config.Config, error) { return config.WithoutZone(cfg, name) })
}

func (m *zoneManager) change(expected string, apply func(config.Config) (config.Config, error)) error {
	if expected != m.digest {
		return routemanager.ErrConfigChanged
	}
	next, err := apply(m.cfg)
	if err != nil {
		return err
	}
	m.cfg, m.digest = next, m.digest+"+"
	return nil
}

type zoneStates []certs.ZoneStatus

func (s zoneStates) Status() []certs.ZoneStatus { return s }
func (s zoneStates) Renew(string) error         { return nil }

// renewer scripts Renew's answer and records the zone asked for.
type renewer struct {
	zoneStates
	err   error
	asked []string
}

func (r *renewer) Renew(zone string) error {
	r.asked = append(r.asked, zone)
	return r.err
}

func zoneTestManager(t *testing.T) *zoneManager {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX absolute-path fixtures")
	}
	cfg, err := config.Validate(config.Config{
		Listen: ":8888",
		Admin:  config.Admin{Token: readToken},
		TLS: config.TLS{Enabled: true, Domains: []string{"*.example.com", "example.com"}, Email: "ops@example.com",
			DNS01: &config.DNS01{StateDir: "/var/lib/ozal/certs", DirectoryURL: "https://acme-v02.api.letsencrypt.org/directory",
				DNSProvider: "builtin", Builtin: &config.DNS01Builtin{Zone: "acme.example.net", Listen: "203.0.113.7:53", Nameservers: []string{"ns1.example.net"}},
				ChallengeDelegation: map[string]string{"example.com": "example-com.acme.example.net"}}},
		Routes: []config.Route{{Host: "www.example.com", Target: "http://127.0.0.1:3000"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &zoneManager{fakeManager: &fakeManager{cfg: *cfg}, digest: "d1"}
}

type zonesBody struct {
	Digest    string     `json:"config_sha256"`
	Available bool       `json:"available"`
	Zones     []zoneView `json:"zones"`
}

func listZonesBody(t *testing.T, handler http.Handler) zonesBody {
	t.Helper()
	rec := request(handler, http.MethodGet, "/_/api/zones", readToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET zones = %d %s", rec.Code, rec.Body)
	}
	var body zonesBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The list shows each zone like a DNS dashboard: where it comes from, its
// certificate state, the record to create and the applications under it.
func TestZonesListShowsStateRecordAndApplications(t *testing.T) {
	manager := zoneTestManager(t)
	notAfter := time.Date(2027, 1, 28, 0, 0, 0, 0, time.UTC)
	handler := New(manager, nil, WithZones(zoneStates{{
		Zone: "example.com", State: certs.ZoneActive,
		Status: certs.Status{Valid: true, NotAfter: notAfter},
	}}))
	body := listZonesBody(t, handler)
	if !body.Available || body.Digest != "d1" || len(body.Zones) != 1 {
		t.Fatalf("body = %+v", body)
	}
	zone := body.Zones[0]
	if zone.Name != "example.com" || zone.Source != "config" || zone.State != "active" || zone.NotAfter != "2027-01-28T00:00:00Z" {
		t.Fatalf("zone = %+v", zone)
	}
	if zone.Challenge != (zoneChallengeView{"_acme-challenge.example.com", "example-com.acme.example.net", true}) {
		t.Fatalf("challenge = %+v", zone.Challenge)
	}
	if len(zone.Hosts) != 1 || zone.Hosts[0] != "www.example.com" {
		t.Fatalf("hosts = %v", zone.Hosts)
	}
}

func TestZonesAddAndRemoveNeedTheReviewedDigest(t *testing.T) {
	manager := zoneTestManager(t)
	handler := New(manager, nil)
	if rec := request(handler, http.MethodPost, "/_/api/zones", readToken, `{"name":"other.dev","expected":"stale"}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale add = %d", rec.Code)
	}
	if rec := request(handler, http.MethodPost, "/_/api/zones", readToken, `{"name":"other.dev"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unreviewed add = %d", rec.Code)
	}
	if rec := request(handler, http.MethodPost, "/_/api/zones", readToken, `{"name":"*.evil.dev","expected":"d1"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wildcard zone = %d", rec.Code)
	}
	rec := request(handler, http.MethodPost, "/_/api/zones", readToken, `{"name":"Other.DEV","expected":"d1"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "zone.added") {
		t.Fatalf("add = %d %s", rec.Code, rec.Body)
	}
	body := listZonesBody(t, handler)
	if len(body.Zones) != 2 || body.Zones[1].Name != "other.dev" || body.Zones[1].Source != "runtime" || body.Zones[1].State != "pending" {
		t.Fatalf("zones = %+v", body.Zones)
	}
	// The builtin server's default delegation is what the operator sees.
	if c := body.Zones[1].Challenge; c.Target != "other.dev.acme.example.net" || !c.Delegated {
		t.Fatalf("challenge = %+v", c)
	}
	if rec := request(handler, http.MethodDelete, "/_/api/zones/example.com", readToken, `{"expected":"`+body.Digest+`"}`); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "tls.domains") {
		t.Fatalf("removing a host-config zone = %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodDelete, "/_/api/zones/other.dev", readToken, `{"expected":"`+body.Digest+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body)
	}
	if body := listZonesBody(t, handler); len(body.Zones) != 1 {
		t.Fatalf("zones after removal = %+v", body.Zones)
	}
}

// Zones are an applications change: no credential, no zones.
func TestZonesNeedTheApplicationsAuthority(t *testing.T) {
	handler := New(zoneTestManager(t), nil)
	if rec := request(handler, http.MethodGet, "/_/api/zones", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d", rec.Code)
	}
	if rec := request(handler, http.MethodPost, "/_/api/zones", "wrong", `{"name":"other.dev","expected":"d1"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token add = %d", rec.Code)
	}
}

// A new zone counts as certificate coverage for applications under it.
func TestApplicationCoverageIncludesZones(t *testing.T) {
	manager := zoneTestManager(t)
	next, err := config.WithZone(manager.cfg, "other.dev")
	if err != nil {
		t.Fatal(err)
	}
	if got := tlsCoverage(manager.cfg, "app.other.dev"); got != "not_covered" {
		t.Fatalf("before the zone: %s", got)
	}
	if got := tlsCoverage(next, "app.other.dev"); got != "configured_coverage_certificate_not_probed" {
		t.Fatalf("after the zone: %s", got)
	}
	if got := tlsCoverage(next, "a.b.other.dev"); got != "not_covered" {
		t.Fatalf("two labels below a zone: %s", got)
	}
}

func TestZoneCheckReturnsTheRecordToCreate(t *testing.T) {
	manager := zoneTestManager(t)
	answers := map[string]string{"_acme-challenge.example.com": "example-com.acme.example.net."}
	var asked []string
	handler := New(manager, nil, WithZoneResolver(func(_ context.Context, host string) (string, error) {
		asked = append(asked, host)
		if target, ok := answers[host]; ok {
			return target, nil
		}
		return host + ".", nil // no CNAME
	}))
	rec := request(handler, http.MethodPost, "/_/api/zones/example.com/check", readToken, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"ok"`) {
		t.Fatalf("delegated zone in place = %d %s", rec.Code, rec.Body)
	}
	next, err := config.WithZone(manager.cfg, "other.dev")
	if err != nil {
		t.Fatal(err)
	}
	manager.cfg = next
	rec = request(handler, http.MethodPost, "/_/api/zones/other.dev/check", readToken, "")
	var body struct {
		Challenge domaincheck.ChallengeCheck `json:"challenge"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Challenge.State != "bad" || body.Challenge.Record == nil || body.Challenge.Record.Content != "other.dev.acme.example.net" {
		t.Fatalf("missing CNAME = %+v", body.Challenge)
	}
	// Only served zones are looked up: the edge never resolves names for anyone.
	if rec := request(handler, http.MethodPost, "/_/api/zones/victim.example/check", readToken, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unserved zone = %d", rec.Code)
	}
	if len(asked) != 2 {
		t.Fatalf("lookups = %v", asked)
	}
}

func TestZoneRenewMapsTheManagerLimits(t *testing.T) {
	manager := zoneTestManager(t)
	for _, c := range []struct {
		err  error
		code int
	}{
		{nil, http.StatusAccepted},
		{certs.ErrAttemptRunning, http.StatusConflict},
		{certs.ErrRenewTooSoon, http.StatusTooManyRequests},
	} {
		r := &renewer{err: c.err}
		handler := New(manager, nil, WithZones(r))
		if rec := request(handler, http.MethodPost, "/_/api/zones/example.com/renew", readToken, ""); rec.Code != c.code {
			t.Errorf("Renew error %v -> %d, want %d", c.err, rec.Code, c.code)
		}
		if len(r.asked) != 1 || r.asked[0] != "example.com" {
			t.Errorf("asked %v", r.asked)
		}
	}
	r := &renewer{}
	if rec := request(New(manager, nil, WithZones(r)), http.MethodPost, "/_/api/zones/other.dev/renew", readToken, ""); rec.Code != http.StatusNotFound || len(r.asked) != 0 {
		t.Fatalf("unserved zone renewed: %d %v", rec.Code, r.asked)
	}
	if rec := request(New(manager, nil), http.MethodPost, "/_/api/zones/example.com/renew", readToken, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("renew without certificates = %d", rec.Code)
	}
}
