package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestPendingPutAndTake(t *testing.T) {
	p := newPendingStore()
	if err := p.put("state1", "verifier1", "/dashboard"); err != nil {
		t.Fatalf("put: %v", err)
	}

	pl, ok := p.take("state1")
	if !ok {
		t.Fatal("expected pending login to be found")
	}
	if pl.codeVerifier != "verifier1" || pl.returnTo != "/dashboard" {
		t.Errorf("unexpected pending login: %+v", pl)
	}
}

func TestPendingTakeIsOneTimeUse(t *testing.T) {
	p := newPendingStore()
	p.put("state1", "verifier1", "/dashboard")

	p.take("state1")
	if _, ok := p.take("state1"); ok {
		t.Fatal("expected second take of the same state to fail")
	}
}

func TestPendingTakeUnknownState(t *testing.T) {
	p := newPendingStore()
	if _, ok := p.take("never-put"); ok {
		t.Fatal("expected unknown state to not be found")
	}
}

func TestPendingPutFailsAtCapacity(t *testing.T) {
	p := newPendingStore()
	for i := 0; i < maxPendingLogins; i++ {
		if err := p.put(fmt.Sprintf("state-%d", i), "verifier", "/"); err != nil {
			t.Fatalf("put #%d: unexpected error before reaching capacity: %v", i, err)
		}
	}

	if err := p.put("one-too-many", "verifier", "/"); !errors.Is(err, ErrTooManyPendingLogins) {
		t.Fatalf("put at capacity: err = %v, want ErrTooManyPendingLogins", err)
	}
}

func TestPendingStartCleanupSweepsExpired(t *testing.T) {
	p := newPendingStore()
	p.byKey["already-expired"] = pendingLogin{
		codeVerifier: "v",
		returnTo:     "/",
		expiresAt:    time.Now().Add(-time.Minute),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.startCleanup(ctx, 10*time.Millisecond)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		_, stillPresent := p.byKey["already-expired"]
		p.mu.Unlock()
		if !stillPresent {
			return // swept, as expected
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected the periodic sweep to evict the expired pending login within 1s")
}
