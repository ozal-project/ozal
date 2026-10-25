package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeployReplayConsumptionIsAtomicAndSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db := migratedSessions(t, path, time.Hour).db
	store := NewDeployReplays(db)
	expiry := time.Now().Add(5 * time.Minute)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			ok, err := store.Consume(ctx, "same-jti", expiry)
			if err != nil {
				t.Error(err)
			}
			if ok {
				successes.Add(1)
			}
		})
	}
	wg.Wait()
	if got := successes.Load(); got != 1 {
		t.Fatalf("successful consumers=%d,want 1", got)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if ok, err := NewDeployReplays(reopened).Consume(ctx, "same-jti", expiry); err != nil || ok {
		t.Fatalf("reopened accepted replay: %v,%v", ok, err)
	}
}

func TestDeployReplayCapacityDoesNotEvictLiveTokens(t *testing.T) {
	ctx := context.Background()
	db := migratedSessions(t, filepath.Join(t.TempDir(), "state.db"), time.Hour).db
	store := NewDeployReplays(db)
	store.capacity = 1
	now := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return now }
	if ok, err := store.Consume(ctx, "first", now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("first=%v,%v", ok, err)
	}
	if _, err := store.Consume(ctx, "second", now.Add(time.Minute)); err == nil {
		t.Fatal("capacity must fail closed")
	}
	if ok, err := store.Consume(ctx, "first", now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("capacity evicted first token: %v,%v", ok, err)
	}
	now = now.Add(2 * time.Minute)
	if ok, err := store.Consume(ctx, "second", now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("expiry did not reclaim capacity: %v,%v", ok, err)
	}
}
