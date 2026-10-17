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

	"github.com/octaviosantacruz/switchboard/internal/config"
)

const defaultPath = "/"

// Status is the latest observed operational state for one route.
type Status struct {
	Healthy        bool
	Checked        bool
	LastCheck      time.Time
	Latency        time.Duration
	LastError      string
	LastTransition time.Time
}

type Checker struct {
	client *http.Client

	mu     sync.RWMutex
	status map[string]Status // route host -> latest probe result
}

func New() *Checker {
	return &Checker{
		client: &http.Client{Timeout: 3 * time.Second},
		status: make(map[string]Status),
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
	var wg sync.WaitGroup
	for _, r := range routes {
		if r.IsStatic() {
			continue // no upstream to probe; served from disk
		}
		wg.Add(1)
		go func(r config.Route) {
			defer wg.Done()
			c.probe(ctx, r)
		}(r)
	}
	wg.Wait()
}

func (c *Checker) probe(ctx context.Context, r config.Route) {
	path := r.HealthPath
	if path == "" {
		path = defaultPath
	}

	started := time.Now()
	healthy := false
	lastError := ""
	probeURL, parseErr := url.Parse(r.Target)
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
	previous, hadPrevious := c.status[r.Host]
	transition := now
	if hadPrevious && previous.Healthy == healthy {
		transition = previous.LastTransition
	}
	c.status[r.Host] = Status{
		Healthy:        healthy,
		Checked:        true,
		LastCheck:      now,
		Latency:        now.Sub(started),
		LastError:      lastError,
		LastTransition: transition,
	}
	c.mu.Unlock()
}
