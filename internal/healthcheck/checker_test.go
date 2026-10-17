package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

func TestHealthyBeforeFirstCheck(t *testing.T) {
	c := New()
	if !c.Healthy("never-checked.localhost") {
		t.Fatal("an unchecked host should be treated as healthy")
	}
}

func TestProbeMarksHealthyAndUnhealthy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()

	c := New()
	routes := []config.Route{
		{Host: "up.localhost", Target: up.URL},
		{Host: "down.localhost", Target: down.URL},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx, routes, time.Hour) // one immediate probe, no need for the ticker to fire in this test

	if !c.Healthy("up.localhost") {
		t.Error("up.localhost should be healthy")
	}
	if c.Healthy("down.localhost") {
		t.Error("down.localhost should be unhealthy")
	}
}

func TestProbeJoinsHealthPathToTheTargetPath(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	checker := New()
	route := config.Route{Host: "app.localhost", Target: upstream.URL + "/prefix", HealthPath: "/healthz"}
	checker.probe(context.Background(), route)

	if gotPath != "/prefix/healthz" {
		t.Fatalf("probed path = %q, want /prefix/healthz", gotPath)
	}
	if status := checker.Status(route.Host); !status.Healthy {
		t.Fatalf("status = %+v, want healthy", status)
	}
}

func TestStatusTracksChecksErrorsAndTransitions(t *testing.T) {
	var responseStatus atomic.Int64
	responseStatus.Store(http.StatusOK)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(responseStatus.Load()))
	}))
	defer upstream.Close()

	checker := New()
	route := config.Route{Host: "app.localhost", Target: upstream.URL}
	checker.probe(context.Background(), route)
	first := checker.Status(route.Host)
	if !first.Checked || !first.Healthy || first.LastCheck.IsZero() || first.LastTransition.IsZero() {
		t.Fatalf("first status = %+v", first)
	}

	time.Sleep(time.Millisecond)
	checker.probe(context.Background(), route)
	second := checker.Status(route.Host)
	if !second.LastTransition.Equal(first.LastTransition) {
		t.Fatalf("stable health changed transition time: first=%s second=%s", first.LastTransition, second.LastTransition)
	}

	responseStatus.Store(http.StatusServiceUnavailable)
	time.Sleep(time.Millisecond)
	checker.probe(context.Background(), route)
	failed := checker.Status(route.Host)
	if failed.Healthy || failed.LastError != "HTTP 503" {
		t.Fatalf("failed status = %+v", failed)
	}
	if !failed.LastTransition.After(second.LastTransition) {
		t.Fatalf("transition time did not advance: before=%s after=%s", second.LastTransition, failed.LastTransition)
	}
}
