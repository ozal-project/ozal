package pipeline

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/policycontext"
	"github.com/ozal-project/ozal/internal/ratelimit"
)

type auditAppenderFunc func(context.Context, events.Event) error

func (f auditAppenderFunc) Append(ctx context.Context, event events.Event) error {
	return f(ctx, event)
}

type stubAllower struct {
	allow bool
	wait  time.Duration
	keys  []string
}

func (s *stubAllower) Allow(k string) (bool, time.Duration) {
	s.keys = append(s.keys, k)
	return s.allow, s.wait
}

func TestRateLimitStage(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	general, auth := &stubAllower{allow: true}, &stubAllower{allow: false, wait: 1500 * time.Millisecond}
	h := Chain(Trust, RateLimit(general, auth, "/_/auth/"))(ok)

	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.9:1234"
		req.Header.Set("X-Forwarded-For", "6.6.6.6")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := do("/app"); rec.Code != http.StatusNoContent {
		t.Fatalf("general path: got %d", rec.Code)
	}
	rec := do("/_/auth/login")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("auth path: got %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2 (1500ms rounded up)", got)
	}
	wantKey := "203.0.113.9|example.com"
	if len(auth.keys) != 1 || auth.keys[0] != wantKey {
		t.Fatalf("auth limiter keyed on %v, want %q", auth.keys, wantKey)
	}
	if len(general.keys) != 1 || general.keys[0] != wantKey {
		t.Fatalf("general limiter keyed on %v, want %q", general.keys, wantKey)
	}
}

func TestRateLimitStageSeparatesRoutes(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	general := &stubAllower{allow: true}
	h := Chain(Trust, RateLimit(general, nil, ""))(ok)

	for _, host := range []string{"a.example.com", "b.example.com"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/app", nil)
		req.Host = host
		req.RemoteAddr = "203.0.113.9:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if len(general.keys) != 2 || general.keys[0] == general.keys[1] {
		t.Fatalf("keys = %v, want one per route", general.keys)
	}
}

func TestRateLimitStageEmitsEvent(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	recorder := &events.Recorder{}
	general := &stubAllower{allow: false, wait: time.Second}
	h := Chain(Trust, RateLimit(general, general, "/_/auth/", WithRateLimitSink(recorder)))(ok)

	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	event, found := recorder.Find(events.AccessRateLimited)
	if !found {
		t.Fatal("no access.rate_limited event")
	}
	if event.Code != "ip_route" || event.RemoteAddr != "203.0.113.9:1234" {
		t.Fatalf("event = %+v", event)
	}
}

func TestRateLimitEventIncludesDeclaredRouteCatalog(t *testing.T) {
	recorder := &events.Recorder{}
	route := config.Route{
		Host: "app.localhost",
		Resource: &config.RouteResource{
			Owner: "platform", DataClass: "account_data", Sensitivity: "restricted",
			RiskyActions: []string{"POST"},
		},
	}
	denied := &stubAllower{allow: false, wait: time.Second}
	h := Chain(Trust, RateLimit(denied, denied, "/_/auth/",
		WithRateLimitSink(recorder), WithRouteCatalog([]config.Route{route})))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodPost, "http://app.localhost/action?private=value", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	event, ok := recorder.Find(events.AccessRateLimited)
	if !ok || event.Context == nil {
		t.Fatalf("rate-limit event missing: %+v", event)
	}
	if event.Context.Resource.Owner.Value == nil || *event.Context.Resource.Owner.Value != "platform" ||
		event.Context.Resource.Sensitivity.Value == nil || *event.Context.Resource.Sensitivity.Value != "restricted" ||
		event.Context.Action.Risky.Value == nil || !*event.Context.Action.Risky.Value {
		t.Fatalf("rate-limit event lost declared catalog: %+v", event.Context)
	}
	if event.Context.Action.PathClass.Value == nil || *event.Context.Action.PathClass.Value != policycontext.PathApplication {
		t.Fatalf("rate-limit event path classification = %+v", event.Context.Action.PathClass)
	}
}

func TestRateLimitAuditFailureFollowsRouteMode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		route      config.Route
		wantStatus int
	}{
		{name: "protected required", route: config.Route{Host: "private.localhost", Target: "http://localhost:9000", Access: &config.AccessPolicy{}}, wantStatus: http.StatusServiceUnavailable},
		{name: "public best effort", route: config.Route{Host: "public.localhost", Target: "http://localhost:9000"}, wantStatus: http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			denied := &stubAllower{allow: false, wait: time.Second}
			recorder := &events.Recorder{}
			appender := auditAppenderFunc(func(_ context.Context, event events.Event) error {
				if event.Type != events.AccessRateLimited {
					t.Fatalf("appended event type = %q", event.Type)
				}
				return errors.New("disk full")
			})
			handler := Chain(Trust, RateLimit(denied, denied, "/_/auth/",
				WithRateLimitSink(recorder), WithRouteCatalog([]config.Route{tc.route}), WithRateLimitAudit(appender)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("rate-limited request reached next handler")
			}))
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.route.Host+"/", nil)
			req.Host = tc.route.Host
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if !recorder.Has(events.AccessRateLimited) {
				t.Fatal("live event was not emitted after audit failure")
			}
		})
	}
}

func TestRouteCatalogPreservesExplicitEmptyRiskyActions(t *testing.T) {
	route := config.Route{
		Host: "app.localhost",
		Resource: &config.RouteResource{
			Owner: "platform", DataClass: "demo", Sensitivity: "public",
			RiskyActions: []string{},
		},
	}
	cfg := rateLimitConfig{}
	WithRouteCatalog([]config.Route{route})(&cfg)
	resource := routeResource(cfg.catalog, "app.localhost")
	context := RequestPolicyContext(httptest.NewRequest(http.MethodPost, "http://app.localhost/", nil), "app.localhost", nil, time.Now().UTC(), resource)
	if context.Action.Risky.Value == nil || *context.Action.Risky.Value {
		t.Fatalf("explicitly empty risky_actions was treated as unknown or risky: %+v", context.Action.Risky)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	tests := []struct {
		wait time.Duration
		want int
	}{
		{0, 1},
		{-time.Second, 1},
		{10 * time.Millisecond, 1},
		{time.Second, 1},
		{time.Second + time.Nanosecond, 2},
		{90 * time.Second, 90},
	}
	for _, tt := range tests {
		if got := RetryAfterSeconds(tt.wait); got != tt.want {
			t.Errorf("RetryAfterSeconds(%v) = %d, want %d", tt.wait, got, tt.want)
		}
	}
}

func TestRateLimitEquivalentHostsShareBudget(t *testing.T) {
	for _, hosts := range [][]string{{"APP.example", "app.example."}, {"[2001:db8::1]", "[2001:0db8:0:0:0:0:0:1]"}} {
		for _, path := range []string{"/", "/_/auth/login"} {
			t.Run(hosts[0]+path, func(t *testing.T) {
				limiter := ratelimit.NewLimiter(0.001, 1, time.Hour)
				handler := Chain(Trust, RateLimit(limiter, limiter, "/_/auth/"))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
				for i, host := range hosts {
					req := httptest.NewRequest(http.MethodGet, path, nil)
					req.Host = host
					req.RemoteAddr = "203.0.113.9:1234"
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					want := http.StatusNoContent
					if i > 0 {
						want = http.StatusTooManyRequests
					}
					if rec.Code != want {
						t.Fatalf("host %s: status %d, want %d", host, rec.Code, want)
					}
				}
			})
		}
	}
}
