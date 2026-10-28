// Package healthcheck probes upstreams in the background and reports
// whether each one is currently healthy.
package healthcheck

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/switchboard-project/switchboard/internal/config"
)

const defaultPath = "/"
const maxConcurrentProbes = 16

// Status is the latest observed operational state for one route.
type Status struct {
	Healthy        bool
	Checked        bool
	LastCheck      time.Time
	Latency        time.Duration
	LastError      string
	LastTransition time.Time
}

// TargetStatus pairs one configured upstream URL with its latest health
// observation. The slice returned by UpstreamStatuses follows config order.
type TargetStatus struct {
	Target string
	Status Status
}

type Checker struct {
	client *http.Client

	mu           sync.RWMutex
	status       map[string]Status            // route host -> aggregate latest state
	upstreams    map[string]map[string]Status // route host -> target -> latest state
	routeTargets map[string][]string
}

func New() *Checker {
	return &Checker{
		client:       &http.Client{Timeout: 3 * time.Second},
		status:       make(map[string]Status),
		upstreams:    make(map[string]map[string]Status),
		routeTargets: make(map[string][]string),
	}
}

// Healthy reports whether the route for the given host last checked as
// healthy. An upstream not yet checked is treated as healthy, so routing
// works immediately at startup before the first probe completes.
func (c *Checker) Healthy(host string) bool {
	return c.Status(host).Healthy
}

// Status returns a copy of the latest probe state. Unchecked routes are
// treated as healthy, matching Healthy.
func (c *Checker) Status(host string) Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	status, checked := c.status[host]
	if !checked {
		return Status{Healthy: true}
	}
	return status
}

// HealthyTarget reports the latest state for one target. A target not yet
// checked is treated as healthy so a newly installed snapshot can serve while
// its first active probe is pending.
func (c *Checker) HealthyTarget(host, target string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	status, checked := c.upstreams[host][target]
	return !checked || status.Healthy
}

// UpstreamStatuses returns a stable, config-ordered copy of one route's
// per-target observations for operator diagnostics.
func (c *Checker) UpstreamStatuses(host string) []TargetStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	targets := c.routeTargets[host]
	statuses := make([]TargetStatus, 0, len(targets))
	for _, target := range targets {
		status, checked := c.upstreams[host][target]
		if !checked {
			status = Status{Healthy: true}
		}
		statuses = append(statuses, TargetStatus{Target: target, Status: status})
	}
	return statuses
}

// ReportFailure records a passive transport failure. Active probes are the
// only mechanism that restores a failed target to service.
func (c *Checker) ReportFailure(host, target string) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recordTargetLocked(host, target, Status{
		Healthy:   false,
		Checked:   true,
		LastCheck: now,
		LastError: "passive upstream failure",
	})
}

// Start probes every route on the given interval until ctx is canceled. It
// runs one round of checks immediately before returning, so status is
// populated before the caller starts serving traffic.
func (c *Checker) Start(ctx context.Context, routes []config.Route, interval time.Duration) {
	c.probeAll(ctx, routes)

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.probeAll(ctx, routes)
			}
		}
	}()
}

func (c *Checker) probeAll(ctx context.Context, routes []config.Route) {
	type probeJob struct {
		route  config.Route
		target string
	}
	total := 0
	for _, route := range routes {
		if !route.IsStatic() && route.Connector == "" {
			total += len(route.UpstreamTargets())
		}
	}
	if total == 0 {
		return
	}
	workers := total
	if workers > maxConcurrentProbes {
		workers = maxConcurrentProbes
	}
	jobs := make(chan probeJob)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for job := range jobs {
				c.probeTarget(ctx, job.route, job.target)
			}
		}()
	}
enqueue:
	for _, r := range routes {
		if r.IsStatic() || r.Connector != "" {
			continue // no upstream to probe; served from disk
		}
		targets := r.UpstreamTargets()
		c.mu.Lock()
		c.routeTargets[r.Host] = targets
		c.mu.Unlock()
		for _, target := range targets {
			select {
			case jobs <- probeJob{route: r, target: target}:
			case <-ctx.Done():
				break enqueue
			}
		}
	}
	close(jobs)
	wg.Wait()
}

func (c *Checker) probe(ctx context.Context, r config.Route) {
	targets := r.UpstreamTargets()
	if len(targets) == 0 {
		return
	}
	c.mu.Lock()
	c.routeTargets[r.Host] = targets
	c.mu.Unlock()
	for _, target := range targets {
		c.probeTarget(ctx, r, target)
	}
}

func (c *Checker) probeTarget(ctx context.Context, r config.Route, target string) {
	path := r.HealthPath
	if path == "" {
		path = defaultPath
	}

	started := time.Now()
	healthy := false
	lastError := ""
	probeURL, parseErr := url.Parse(target)
	if parseErr != nil {
		lastError = parseErr.Error()
	} else if req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL.JoinPath(path).String(), nil); err != nil {
		lastError = err.Error()
	} else {
		resp, requestErr := c.client.Do(req)
		if requestErr != nil {
			lastError = requestErr.Error()
		} else {
			resp.Body.Close()
			healthy = resp.StatusCode < http.StatusInternalServerError
			if !healthy {
				lastError = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
		}
	}
	now := time.Now()

	c.mu.Lock()
	c.recordTargetLocked(r.Host, target, Status{
		Healthy:   healthy,
		Checked:   true,
		LastCheck: now,
		Latency:   now.Sub(started),
		LastError: lastError,
	})
	c.mu.Unlock()
}

func (c *Checker) recordTargetLocked(host, target string, status Status) {
	targets := c.routeTargets[host]
	if !containsTarget(targets, target) {
		targets = append(targets, target)
		c.routeTargets[host] = targets
	}
	perTarget := c.upstreams[host]
	if perTarget == nil {
		perTarget = make(map[string]Status)
		c.upstreams[host] = perTarget
	}
	previous, hadPrevious := perTarget[target]
	now := status.LastCheck
	if now.IsZero() {
		now = time.Now()
		status.LastCheck = now
	}
	if hadPrevious && previous.Healthy == status.Healthy {
		status.LastTransition = previous.LastTransition
	} else {
		status.LastTransition = now
	}
	perTarget[target] = status

	aggregate := Status{Healthy: false, Checked: true}
	for _, candidate := range targets {
		observed, checked := perTarget[candidate]
		if !checked || !observed.Checked {
			// Unchecked targets remain eligible until the first probe completes.
			aggregate.Healthy = true
			aggregate.Checked = false
			continue
		}
		aggregate.Healthy = aggregate.Healthy || observed.Healthy
		if observed.LastCheck.After(aggregate.LastCheck) {
			aggregate.LastCheck = observed.LastCheck
		}
		if observed.Latency > aggregate.Latency {
			aggregate.Latency = observed.Latency
		}
		if aggregate.LastError == "" && !observed.Healthy {
			aggregate.LastError = observed.LastError
		}
	}
	previousRoute, hadPreviousRoute := c.status[host]
	if hadPreviousRoute && previousRoute.Healthy == aggregate.Healthy {
		aggregate.LastTransition = previousRoute.LastTransition
	} else {
		aggregate.LastTransition = now
	}
	if aggregate.Healthy {
		aggregate.LastError = ""
	}
	c.status[host] = aggregate
}

func containsTarget(targets []string, target string) bool {
	for _, candidate := range targets {
		if candidate == target {
			return true
		}
	}
	return false
}
