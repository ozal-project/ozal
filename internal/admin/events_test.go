package admin

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/events"
)

func newStreamServer(t *testing.T, broker *events.Broker) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(&fakeManager{}, broker, WithEvents(broker)))
	t.Cleanup(srv.Close)
	return srv
}

func TestEventStreamReplaysAndStreams(t *testing.T) {
	broker := events.NewBroker()
	broker.Emit(events.Event{Type: events.LoginStarted, Host: "app.localhost"})
	srv := newStreamServer(t, broker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/_/events", nil)
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
		if _, _, c, ok := broker.Subscribe(); ok {
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
