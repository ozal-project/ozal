package healthcheck

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

type probeRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f probeRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

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

func TestMultipleTargetsHaveIndependentActiveAndPassiveHealth(t *testing.T) {
	responses := map[string]int{"one.local": http.StatusServiceUnavailable, "two.local": http.StatusOK}
	checker := New()
	checker.client.Transport = probeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: responses[request.URL.Host],
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})
	route := config.Route{
		Host:      "app.localhost",
		Upstreams: []string{"http://one.local", "http://two.local"},
	}
	checker.probe(context.Background(), route)
	if checker.HealthyTarget(route.Host, route.Upstreams[0]) {
		t.Fatal("target returning 503 should be unhealthy")
	}
	if !checker.HealthyTarget(route.Host, route.Upstreams[1]) || !checker.Healthy(route.Host) {
		t.Fatal("healthy peer should keep the route available")
	}
	perTarget := checker.UpstreamStatuses(route.Host)
	if len(perTarget) != 2 || perTarget[0].Target != route.Upstreams[0] || perTarget[0].Status.Healthy || perTarget[1].Target != route.Upstreams[1] || !perTarget[1].Status.Healthy {
		t.Fatalf("per-target statuses = %+v", perTarget)
	}
	if status := checker.Status(route.Host); !status.Checked || !status.Healthy {
		t.Fatalf("aggregate status = %+v, want checked and healthy", status)
	}

	checker.ReportFailure(route.Host, route.Upstreams[1])
	if checker.Healthy(route.Host) {
		t.Fatal("route should be unhealthy after both targets fail")
	}
	responses["one.local"] = http.StatusOK
	checker.probeTarget(context.Background(), route, route.Upstreams[0])
	if !checker.HealthyTarget(route.Host, route.Upstreams[0]) || !checker.Healthy(route.Host) {
		t.Fatal("successful active probe should restore the target and route")
	}
}

func TestProbeAllBoundsConcurrencyAcrossTargets(t *testing.T) {
	const targetCount = 40
	var active atomic.Int64
	var peak atomic.Int64
	checker := New()
	checker.client.Transport = probeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		current := active.Add(1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})
	targets := make([]string, targetCount)
	for i := range targets {
		targets[i] = fmt.Sprintf("http://upstream-%02d.local", i)
	}
	checker.probeAll(context.Background(), []config.Route{{Host: "app.localhost", Upstreams: targets}})
	if got := peak.Load(); got < 2 || got > maxConcurrentProbes {
		t.Fatalf("peak concurrent probes = %d, want between 2 and %d", got, maxConcurrentProbes)
	}
	if status := checker.Status("app.localhost"); !status.Checked || !status.Healthy {
		t.Fatalf("aggregate status = %+v, want checked and healthy", status)
	}
}
