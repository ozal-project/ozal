package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/policycontext"
)

func TestEventViewPreservesDecisionContext(t *testing.T) {
	decisionContext := policycontext.Build(policycontext.Input{
		ObservedAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		PathClass:  policycontext.Known(policycontext.PathApplication, policycontext.SourceClassifier),
	})
	data, err := json.Marshal(toView("epoch-1", events.Event{Type: events.AccessAllowed, Context: &decisionContext}))
	if err != nil {
		t.Fatalf("marshal event view: %v", err)
	}
	var view map[string]any
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatalf("decode event view: %v", err)
	}
	context, ok := view["context"].(map[string]any)
	if !ok || context["version"] != float64(policycontext.Version) {
		t.Fatalf("event view dropped decision context: %s", data)
	}
}

func TestEventViewOmitsRawError(t *testing.T) {
	data, err := json.Marshal(toView("epoch-1", events.Event{
		Type: events.LoginFailed, Code: "token_exchange_failed", Error: "/private/provider: secret-token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/private/provider") || strings.Contains(string(data), "secret-token") {
		t.Fatalf("event view exposed a raw error: %s", data)
	}
	var view map[string]any
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	if _, ok := view["error"]; ok {
		t.Fatalf("event view retained an error field: %s", data)
	}
}

func newStreamServer(t *testing.T, broker *events.Broker) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: "stream-secret"}}}, broker, WithEvents(broker)))
	t.Cleanup(srv.Close)
	return srv
}

func TestEventStreamRequiresAuthentication(t *testing.T) {
	broker := events.NewBroker()
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: "stream-secret"}}}, broker, WithEvents(broker))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/_/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous event stream status = %d, want 401", rec.Code)
	}
}

func TestRevokedOperatorCannotContinueReadingAnOpenStream(t *testing.T) {
	broker := events.NewBroker()
	broker.Emit(events.Event{Type: events.LoginStarted})
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: "stream-secret"}}}, broker, WithEvents(broker))
	token, grant, err := h.operators.issue("Ana", false, "people")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/_/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	lines := bufio.NewScanner(resp.Body)
	for lines.Scan() {
		if strings.HasPrefix(lines.Text(), "data: ") {
			break
		}
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	h.operators.revoke(grant.ID)
	broker.Emit(events.Event{Type: events.AccessDenied, Host: "after-revocation"})
	for lines.Scan() {
		if strings.Contains(lines.Text(), "after-revocation") {
			t.Fatal("revoked session received an event")
		}
	}
	if err := lines.Err(); err != nil {
		t.Fatalf("stream did not close after revocation: %v", err)
	}
}

func TestEventStreamReplaysAndStreams(t *testing.T) {
	broker := events.NewBroker()
	broker.Emit(events.Event{Type: events.LoginStarted, Host: "app.localhost"})
	srv := newStreamServer(t, broker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/_/events", nil)
	req.Header.Set("Authorization", "Bearer stream-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	lines := bufio.NewScanner(resp.Body)
	next := func() string {
		t.Helper()
		for lines.Scan() {
			if strings.HasPrefix(lines.Text(), "data: ") {
				return lines.Text()
			}
		}
		t.Fatalf("stream ended: %v", lines.Err())
		return ""
	}
	if got := next(); !strings.Contains(got, `"type":"login.started"`) {
		t.Fatalf("replayed event = %s", got)
	}
	broker.Emit(events.Event{Type: events.AccessDenied, Reason: "no <b>claims</b>"})
	got := next()
	if !strings.Contains(got, `"type":"access.denied"`) {
		t.Fatalf("live event = %s", got)
	}
}

func TestEventStreamEndsWhenClientLeaves(t *testing.T) {
	broker := events.NewBroker()
	srv := newStreamServer(t, broker)
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/_/events", nil)
	req.Header.Set("Authorization", "Bearer stream-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()

	// The handler must unsubscribe: with the cap at 16, leaking one stream
	// per connection would eventually refuse everyone.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, c, ok := broker.Subscribe(0); ok {
			c()
			if runtime.NumGoroutine() <= before+2 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stream goroutine or subscription leaked after client left")
}

func TestActivityPageIsLoopbackOnlyAndStrictCSP(t *testing.T) {
	broker := events.NewBroker()
	h := New(&fakeManager{}, broker, WithEvents(broker))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:9090/activity", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if rec.Code != 200 || !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe") {
		t.Fatalf("activity page: %d %v", rec.Code, rec.Header())
	}

	// The old server-rendered URL keeps working as a redirect.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:9090/_/activity", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/activity" {
		t.Fatalf("old activity URL: %d %v", rec.Code, rec.Header())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://evil.example/_/events", nil))
	if rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("non-loopback host: got %d", rec.Code)
	}
}

func TestActivityRoutesAbsentWithoutBroker(t *testing.T) {
	h := New(&fakeManager{}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:9090/_/events", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

// readEvents opens the stream with an optional Last-Event-ID and returns the
// first n (id, data) pairs.
func readEvents(t *testing.T, url, lastID string, n int) [][2]string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/_/events", nil)
	req.Header.Set("Authorization", "Bearer stream-secret")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	var out [][2]string
	id := ""
	for len(out) < n && lines.Scan() {
		switch line := lines.Text(); {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			out = append(out, [2]string{id, strings.TrimPrefix(line, "data: ")})
			id = ""
		}
	}
	return out
}

// BUG-009: a reconnecting console gets only the events after the last one it
// saw, not the whole history again, and every event carries its identity.
func TestEventStreamResumesFromLastEventID(t *testing.T) {
	broker := events.NewBroker()
	broker.Emit(events.Event{Type: events.LoginStarted})
	broker.Emit(events.Event{Type: events.AccessDenied})
	srv := newStreamServer(t, broker)

	first := readEvents(t, srv.URL, "", 2)
	if len(first) != 2 || first[0][0] != broker.Epoch()+"-1" || first[1][0] != broker.Epoch()+"-2" {
		t.Fatalf("first connection = %v", first)
	}
	if !strings.Contains(first[0][1], `"id":"`+first[0][0]+`"`) {
		t.Fatalf("the data does not carry its id: %s", first[0][1])
	}

	broker.Emit(events.Event{Type: events.AccessAllowed})
	again := readEvents(t, srv.URL, first[0][0], 2)
	if len(again) != 2 || again[0][0] != first[1][0] || !strings.Contains(again[1][1], `"type":"access.allowed"`) {
		t.Fatalf("after reconnecting from %s = %v, want events 2 and 3 only", first[0][0], again)
	}

	// An ID from another process (a restart) cannot resume: everything again.
	other := readEvents(t, srv.URL, "0123456789abcdef-2", 3)
	if len(other) != 3 || other[0][0] != broker.Epoch()+"-1" {
		t.Fatalf("another epoch's ID = %v, want the whole history", other)
	}
}

func TestResumeAfterOnlyTrustsItsOwnEpoch(t *testing.T) {
	for _, tt := range []struct {
		id   string
		want uint64
	}{
		{"ep-7", 7}, {"other-7", 0}, {"", 0}, {"ep", 0}, {"ep-x", 0}, {"ep--1", 0},
	} {
		if got := resumeAfter(tt.id, "ep"); got != tt.want {
			t.Errorf("resumeAfter(%q) = %d, want %d", tt.id, got, tt.want)
		}
	}
}
