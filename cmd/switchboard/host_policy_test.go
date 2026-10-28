package main

import (
	"context"
	"sync"
	"testing"

	"github.com/switchboard-project/switchboard/internal/config"
)

// The HTTP-01 listener follows the route table: a hostname added in the
// console gets a certificate without a restart, one never configured or
// since removed is refused before the CA is contacted.
func TestRouteHostPolicyFollowsTheCurrentRoutes(t *testing.T) {
	var mu sync.Mutex
	cfg := config.Config{Routes: []config.Route{{Host: "app.example.com"}}}
	current := func() config.Config { mu.Lock(); defer mu.Unlock(); return cfg }
	policy := routeHostPolicy([]string{"edge.example.com"}, current)
	ctx := context.Background()

	for host, allowed := range map[string]bool{"edge.example.com": true, "app.example.com": true, "new.example.com": false, "attacker.example": false} {
		if err := policy(ctx, host); (err == nil) != allowed {
			t.Fatalf("policy(%q) err = %v, want allowed=%v", host, err, allowed)
		}
	}
	mu.Lock()
	cfg = config.Config{Routes: []config.Route{{Host: "new.example.com"}}}
	mu.Unlock()
	if err := policy(ctx, "new.example.com"); err != nil {
		t.Fatalf("a route added after startup was refused: %v", err)
	}
	if err := policy(ctx, "app.example.com"); err == nil {
		t.Fatal("a removed route still gets certificates")
	}
	if err := policy(ctx, "edge.example.com"); err != nil {
		t.Fatalf("a configured certificate name stopped being allowed: %v", err)
	}
}
