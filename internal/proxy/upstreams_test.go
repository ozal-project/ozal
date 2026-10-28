package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/switchboard-project/switchboard/internal/auth"
	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
)

type targetChecker struct {
	mu       sync.Mutex
	healthy  map[string]bool
	failures map[string]int
}

func (c *targetChecker) Healthy(string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, healthy := range c.healthy {
		if healthy {
			return true
		}
	}
	return len(c.healthy) == 0
}

func (c *targetChecker) HealthyTarget(_, target string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	healthy, checked := c.healthy[target]
	return !checked || healthy
}

func (c *targetChecker) ReportFailure(_, target string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.healthy[target] = false
	c.failures[target]++
}

type selectionUnavailableChecker struct{}

func (selectionUnavailableChecker) Healthy(string) bool            { return true }
func (selectionUnavailableChecker) HealthyTarget(_, _ string) bool { return false }

type eventSinkFunc func(events.Event)

func (f eventSinkFunc) Emit(event events.Event) { f(event) }

func TestMultipleUpstreamsRoundRobin(t *testing.T) {
	var calls []string
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.URL.Host)
		return upstreamResponse(req, http.StatusOK, req.URL.Host), nil
	})
	handler, err := New([]config.Route{{
		Host:      "app.localhost",
		Upstreams: []string{"http://one.local", "http://two.local"},
	}}, nil, nil, WithTransport(transport))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 4; i++ {
		if got := serveUpstreamRequest(handler, "app.localhost").Code; got != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i, got)
		}
	}
	if want := []string{"one.local", "two.local", "one.local", "two.local"}; !equalStrings(calls, want) {
		t.Fatalf("upstream order = %v, want %v", calls, want)
	}
}

func TestPassiveFailureRemovesUpstreamFromNewRequests(t *testing.T) {
	checker := &targetChecker{
		healthy:  map[string]bool{"http://one.local": true, "http://two.local": true},
		failures: make(map[string]int),
	}
	var mu sync.Mutex
	var calls []string
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		calls = append(calls, req.URL.Host)
		mu.Unlock()
		if req.URL.Host == "one.local" {
			return nil, errors.New("connection refused")
		}
		return upstreamResponse(req, http.StatusOK, "two"), nil
	})
	handler, err := New([]config.Route{{
		Host:      "app.localhost",
		Upstreams: []string{"http://one.local", "http://two.local"},
	}}, checker, nil, WithTransport(transport))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i, wantStatus := range []int{http.StatusBadGateway, http.StatusOK, http.StatusOK} {
		if got := serveUpstreamRequest(handler, "app.localhost").Code; got != wantStatus {
			t.Fatalf("request %d status = %d, want %d", i, got, wantStatus)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"one.local", "two.local", "two.local"}; !equalStrings(calls, want) {
		t.Fatalf("upstream order after passive failure = %v, want %v", calls, want)
	}
	if checker.failures["http://one.local"] != 1 {
		t.Fatalf("passive failure count = %d, want 1", checker.failures["http://one.local"])
	}
}

func TestCanceledClientDoesNotMarkSingleUpstreamUnhealthy(t *testing.T) {
	const target = "http://one.local"
	checker := &targetChecker{
		healthy:  map[string]bool{target: true},
		failures: make(map[string]int),
	}
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		return upstreamResponse(req, http.StatusOK, "healthy"), nil
	})
	handler, err := New([]config.Route{{
		Host:   "app.localhost",
		Target: target,
	}}, checker, nil, WithTransport(transport))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	canceled := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
	ctx, cancel := context.WithCancel(canceled.Context())
	cancel()
	handler.ServeHTTP(httptest.NewRecorder(), canceled.WithContext(ctx))

	if checker.failures[target] != 0 {
		t.Fatalf("client cancellation recorded %d passive failures, want 0", checker.failures[target])
	}
	if !checker.healthy[target] {
		t.Fatal("client cancellation marked the only upstream unhealthy")
	}
	if got := serveUpstreamRequest(handler, "app.localhost").Code; got != http.StatusOK {
		t.Fatalf("next request status = %d, want 200 after client cancellation", got)
	}
}

func TestUnavailableUpstreamIsDetectedBeforeAccessAllowedEvent(t *testing.T) {
	var eventTypes []events.Type
	sink := eventSinkFunc(func(event events.Event) { eventTypes = append(eventTypes, event.Type) })
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		t.Errorf("request reached %s when no healthy upstream was available", req.URL.Host)
		return nil, errors.New("unexpected forwarding")
	})
	handler, err := New([]config.Route{{
		Host:      "private.localhost",
		Upstreams: []string{"http://one.local", "http://two.local"},
		Access:    &config.AccessPolicy{},
	}}, selectionUnavailableChecker{}, &fakeGate{
		authenticated: true,
		principal:     auth.Principal{Subject: "user-1"},
	}, WithTransport(transport), WithEventSink(sink))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := serveUpstreamRequest(handler, "private.localhost").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", got)
	}
	for _, eventType := range eventTypes {
		if eventType == events.AccessAllowed {
			t.Fatalf("access.allowed was emitted for a request that got 503: %v", eventTypes)
		}
	}
}

func TestUpstreamFailureLogsRedactTransportTextAndURLSecrets(t *testing.T) {
	const rawError = "GET /private/provider?token=secret-error: dial tcp 198.51.100.4: connection refused"
	var logs bytes.Buffer
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(rawError)
	})
	handler, err := New([]config.Route{{
		Host:   "app.localhost",
		Target: "http://operator:secret-password@upstream.local/private/path?api_key=secret-query",
	}}, nil, nil,
		WithTransport(transport),
		WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := serveUpstreamRequest(handler, "app.localhost").Code; got != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", got)
	}
	output := logs.String()
	for _, secret := range []string{rawError, "secret-password", "secret-query", "/private"} {
		if strings.Contains(output, secret) {
			t.Fatalf("structured log exposed %q: %s", secret, output)
		}
	}
	for _, want := range []string{`"target":"http://upstream.local"`, `"target_host":"upstream.local"`, `"failure_class":"transport"`} {
		if !strings.Contains(output, want) {
			t.Errorf("structured log missing %s: %s", want, output)
		}
	}
}

func serveUpstreamRequest(handler http.Handler, host string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
	request.Host = host
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func upstreamResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
