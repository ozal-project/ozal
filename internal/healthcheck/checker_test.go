package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
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
