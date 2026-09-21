// Package healthcheck probes upstreams in the background and reports
// whether each one is currently healthy.
package healthcheck

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

const defaultPath = "/"

type Checker struct {
	client *http.Client

	mu     sync.RWMutex
	status map[string]bool // route host -> healthy
}

func New() *Checker {
	return &Checker{
		client: &http.Client{Timeout: 3 * time.Second},
		status: make(map[string]bool),
	}
}

// Healthy reports whether the route for the given host last checked as
// healthy. An upstream not yet checked is treated as healthy, so routing
// works immediately at startup before the first probe completes.
func (c *Checker) Healthy(host string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	healthy, checked := c.status[host]
	return !checked || healthy
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Target+path, nil)
	healthy := false
	if err == nil {
		resp, err := c.client.Do(req)
		if err == nil {
			healthy = resp.StatusCode < 500
			resp.Body.Close()
		}
	}

	c.mu.Lock()
	c.status[r.Host] = healthy
	c.mu.Unlock()
}
