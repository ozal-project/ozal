package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

// fakeEdge answers the four admin endpoints `app alias` uses and records the
// mutating calls in order.
type fakeEdge struct {
	mu        sync.Mutex
	routes    []config.Route
	provider  string
	tls       bool
	coverage  string
	dnsStatus int // answer of POST .../dns
	checks    []connectionReport
	calls     []string
}

func readyReport() connectionReport {
	r := connectionReport{Connected: true}
	r.DNS.State, r.Certificate.State, r.Reach.State = "ok", "ok", "ok"
	return r
}

func pendingReport() connectionReport {
	r := connectionReport{}
	r.DNS.State, r.Certificate.State, r.Reach.State = "pending", "ok", "pending"
	return r
}

func (f *fakeEdge) start(t *testing.T) *adminClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply := func(status int, v any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /_/api/setup":
			reply(200, map[string]any{"config_sha256": "d1", "tls_enabled": f.tls, "routes": f.routes,
				"dns": map[string]string{"provider": f.provider}})
		case "POST /_/api/apps/plan":
			reply(200, map[string]string{"tls": f.coverage})
		case "POST /_/api/apps":
			var req struct {
				Route    config.Route `json:"route"`
				Expected string       `json:"expected"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Expected != "d1" {
				reply(409, map[string]string{"error": "configuration changed"})
				return
			}
			f.calls = append(f.calls, "save "+req.Route.Host)
			f.routes = append(f.routes, req.Route)
			reply(200, map[string]string{"status": "saved"})
		case "POST /_/api/apps/ozal.example.com/check":
			f.calls = append(f.calls, "check")
			report := pendingReport()
			if len(f.checks) > 0 {
				report, f.checks = f.checks[0], f.checks[1:]
			}
			reply(200, report)
		case "POST /_/api/apps/ozal.example.com/dns":
			f.calls = append(f.calls, "dns")
			if f.dnsStatus >= 300 {
				reply(f.dnsStatus, map[string]string{"error": "this hostname already has a DNS record"})
				return
			}
			reply(201, map[string]string{"type": "A"})
		default:
			reply(404, map[string]string{"error": r.URL.Path})
		}
	}))
	t.Cleanup(srv.Close)
	return &adminClient{client: srv.Client(), base: srv.URL}
}

func newFakeEdge() *fakeEdge {
	return &fakeEdge{
		routes:    []config.Route{{Host: "old.example.com", Site: "main"}},
		provider:  "cloudflare",
		tls:       true,
		coverage:  "configured_coverage_certificate_not_probed",
		dnsStatus: 201,
	}
}

func alias(f *fakeEdge, t *testing.T, mutate func(*aliasRequest)) (string, error) {
	t.Helper()
	req := aliasRequest{From: "old.example.com", To: "ozal.example.com", Apply: true, Wait: time.Second, Every: time.Millisecond}
	if mutate != nil {
		mutate(&req)
	}
	var out bytes.Buffer
	err := aliasApplication(context.Background(), &out, f.start(t), req)
	return out.String(), err
}

func TestAliasPlanChangesNothing(t *testing.T) {
	f := newFakeEdge()
	out, err := alias(f, t, func(r *aliasRequest) { r.Apply = false })
	if err != nil || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
	if !strings.Contains(out, "re-run with -apply") {
		t.Fatalf("plan output: %s", out)
	}
}

func TestAliasSavesThenCreatesDNSThenWaitsForConnection(t *testing.T) {
	f := newFakeEdge()
	f.checks = []connectionReport{pendingReport(), pendingReport(), readyReport()}
	out, err := alias(f, t, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.Join(f.calls, ","); got != "save ozal.example.com,check,dns,check,check" {
		t.Fatalf("calls: %s", got)
	}
	if len(f.routes) != 2 || f.routes[0].Host != "old.example.com" || f.routes[1].Site != "main" {
		t.Fatalf("the source must stay and the alias must copy its site: %+v", f.routes)
	}
}

func TestAliasCopiesPublicReverseProxyRoute(t *testing.T) {
	source := config.Route{
		Host:       "old.example.com",
		Upstreams:  []string{"http://127.0.0.1:8080", "http://127.0.0.1:8081"},
		HealthPath: "/ready", AuditMode: "best_effort",
	}
	got, exists, err := aliasRoute([]config.Route{source}, source.Host, "new.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if exists || got.Host != "new.example.com" || got.Target != source.Target ||
		strings.Join(got.Upstreams, ",") != strings.Join(source.Upstreams, ",") ||
		got.HealthPath != source.HealthPath || got.AuditMode != source.AuditMode {
		t.Fatalf("alias did not preserve the public proxy route: source=%+v alias=%+v exists=%v", source, got, exists)
	}
	if source.Host != "old.example.com" {
		t.Fatalf("alias changed the original route: %+v", source)
	}
}

func TestAliasAlreadyConnectedCreatesNoRecord(t *testing.T) {
	f := newFakeEdge()
	f.checks = []connectionReport{readyReport()}
	if _, err := alias(f, t, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.calls, ","); got != "save ozal.example.com,check" {
		t.Fatalf("calls: %s", got)
	}
}

func TestAliasRerunDoesNotSaveTwice(t *testing.T) {
	f := newFakeEdge()
	f.routes = append(f.routes, config.Route{Host: "ozal.example.com", Site: "main"})
	f.checks = []connectionReport{readyReport()}
	if _, err := alias(f, t, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.calls, ","); got != "check" {
		t.Fatalf("calls: %s", got)
	}
}

func TestAliasExistingRecordIsWaitedForNotReplaced(t *testing.T) {
	f := newFakeEdge()
	f.dnsStatus = http.StatusConflict
	f.checks = []connectionReport{pendingReport(), readyReport()}
	if out, err := alias(f, t, nil); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestAliasWithoutProviderChangesNothing(t *testing.T) {
	f := newFakeEdge()
	f.provider = ""
	_, err := alias(f, t, nil)
	if err == nil || !strings.Contains(err.Error(), "no DNS provider") || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

func TestAliasManualDNSSavesButNeverCreatesTheRecord(t *testing.T) {
	f := newFakeEdge()
	f.provider = ""
	_, err := alias(f, t, func(r *aliasRequest) { r.ManualDNS = true })
	if err == nil || !strings.Contains(err.Error(), "DNS was not modified") {
		t.Fatalf("err=%v", err)
	}
	if got := strings.Join(f.calls, ","); got != "save ozal.example.com,check" {
		t.Fatalf("calls: %s", got)
	}
}

func TestAliasRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*fakeEdge)
		want   string
	}{
		"private source":      {func(f *fakeEdge) { f.routes[0].AuthRequired = true }, "public application"},
		"missing destination": {func(f *fakeEdge) { f.routes[0].Site = "" }, "serving destination"},
		"redirect source": {func(f *fakeEdge) {
			f.routes[0] = config.Route{Host: "old.example.com", Redirect: "https://elsewhere.example"}
		}, "public application"},
		"missing source": {func(f *fakeEdge) { f.routes[0].Host = "other.example.com" }, "public application"},
		"other route at destination": {func(f *fakeEdge) {
			f.routes = append(f.routes, config.Route{Host: "ozal.example.com", Site: "different"})
		}, "different route"},
		"no https":  {func(f *fakeEdge) { f.tls = false }, "HTTPS coverage"},
		"uncovered": {func(f *fakeEdge) { f.coverage = "not_covered" }, "HTTPS coverage"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeEdge()
			tc.mutate(f)
			_, err := alias(f, t, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v", err)
			}
			if len(f.calls) != 0 {
				t.Fatalf("a refusal must not change anything: %v", f.calls)
			}
		})
	}
}

func TestAliasReportsPendingConnectionKeepingTheRoute(t *testing.T) {
	f := newFakeEdge()
	_, err := alias(f, t, func(r *aliasRequest) { r.Wait = 5 * time.Millisecond })
	if err == nil || !strings.Contains(err.Error(), "not connected yet") {
		t.Fatalf("err=%v", err)
	}
	if len(f.routes) != 2 {
		t.Fatal("the route must be kept for reconciliation")
	}
}
