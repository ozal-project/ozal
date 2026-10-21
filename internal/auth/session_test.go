package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

func TestCreateAndGet(t *testing.T) {
	store := NewSessionStore(Lifetime{Absolute: time.Hour})

	id, err := store.Create(context.Background(), Principal{Email: "user@example.com", Subject: "sub-123"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	sess, ok := store.Get(context.Background(), id)
	if !ok {
		t.Fatal("expected session to be found")
	}
	if sess.Principal.Email != "user@example.com" || sess.Principal.Subject != "sub-123" {
		t.Errorf("unexpected session contents: %+v", sess)
	}
}

func TestGetUnknownID(t *testing.T) {
	store := NewSessionStore(Lifetime{Absolute: time.Hour})
	if _, ok := store.Get(context.Background(), "does-not-exist"); ok {
		t.Fatal("expected unknown session id to not be found")
	}
}

func TestExpiredSessionIsEvicted(t *testing.T) {
	store := NewSessionStore(Lifetime{Absolute: -time.Second}) // already expired on creation
	id, err := store.Create(context.Background(), Principal{Email: "user@example.com", Subject: "sub-123"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, ok := store.Get(context.Background(), id); ok {
		t.Fatal("expected an expired session to not be found")
	}
}

func TestDelete(t *testing.T) {
	store := NewSessionStore(Lifetime{Absolute: time.Hour})
	id, _ := store.Create(context.Background(), Principal{Email: "user@example.com", Subject: "sub-123"})

	store.Delete(context.Background(), id)

	if _, ok := store.Get(context.Background(), id); ok {
		t.Fatal("expected session to be gone after Delete")
	}
}

func TestSessionIDsAreUnique(t *testing.T) {
	store := NewSessionStore(Lifetime{Absolute: time.Hour})
	id1, _ := store.Create(context.Background(), Principal{Email: "a@example.com", Subject: "a"})
	id2, _ := store.Create(context.Background(), Principal{Email: "b@example.com", Subject: "b"})
	if id1 == id2 {
		t.Fatal("expected distinct session ids")
	}
}

func TestCreateFailsAtCapacity(t *testing.T) {
	store := NewSessionStore(Lifetime{Absolute: time.Hour})
	for i := 0; i < MaxSessions; i++ {
		if _, err := store.Create(context.Background(), Principal{Email: "user@example.com", Subject: "sub"}); err != nil {
			t.Fatalf("Create #%d: unexpected error before reaching capacity: %v", i, err)
		}
	}

	if _, err := store.Create(context.Background(), Principal{Email: "one-too-many@example.com", Subject: "sub"}); !errors.Is(err, ErrStoreFull) {
		t.Fatalf("Create at capacity: err = %v, want ErrStoreFull", err)
	}
}

func TestSweepLoopSweepsExpiredSessions(t *testing.T) {
	store := NewSessionStore(Lifetime{Absolute: -time.Second}) // already expired on creation
	id, err := store.Create(context.Background(), Principal{Email: "user@example.com", Subject: "sub-123"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go SweepLoop(ctx, store, 10*time.Millisecond)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		_, stillPresent := store.sessions[id]
		store.mu.Unlock()
		if !stillPresent {
			return // swept, as expected
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected the periodic sweep to evict the expired session within 1s")
}

// countingBackend notices sweeps and can prove the loop stopped.
type countingBackend struct {
	sweeps chan struct{}
	done   chan struct{}
}

func (b *countingBackend) Create(context.Context, Principal) (string, error) { return "", nil }
func (b *countingBackend) Get(context.Context, string) (Session, bool)       { return Session{}, false }
func (b *countingBackend) Delete(context.Context, string)                    {}

func (b *countingBackend) Sweep(ctx context.Context) {
	select {
	case b.sweeps <- struct{}{}:
	default:
	}
}

func TestSweepLoopStopsWithContext(t *testing.T) {
	backend := &countingBackend{sweeps: make(chan struct{}, 1), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		SweepLoop(ctx, backend, time.Millisecond)
		close(backend.done)
	}()
	select {
	case <-backend.sweeps:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("the sweep loop never ran")
	}
	cancel()
	select {
	case <-backend.done:
	case <-time.After(time.Second):
		t.Fatal("the sweep loop did not stop when its context was cancelled")
	}
}

// The rule both backends apply: the earlier of the idle deadline and the
// absolute deadline.
func TestExpiryIsTheEarlierOfIdleAndAbsolute(t *testing.T) {
	login := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	week := Lifetime{Idle: 12 * time.Hour, Absolute: 7 * 24 * time.Hour}
	for name, tc := range map[string]struct {
		lastSeen time.Time
		lifetime Lifetime
		want     time.Time
	}{
		"fresh login: idle first":      {login, week, login.Add(12 * time.Hour)},
		"used later: idle moves":       {login.Add(30 * time.Hour), week, login.Add(42 * time.Hour)},
		"busy near the end: absolute":  {login.Add(160 * time.Hour), week, login.Add(168 * time.Hour)},
		"no idle limit: absolute only": {login, Lifetime{Absolute: 8 * time.Hour}, login.Add(8 * time.Hour)},
		"idle longer than absolute":    {login, Lifetime{Idle: 48 * time.Hour, Absolute: 8 * time.Hour}, login.Add(8 * time.Hour)},
	} {
		t.Run(name, func(t *testing.T) {
			if got := expiry(login, tc.lastSeen, tc.lifetime); !got.Equal(tc.want) {
				t.Fatalf("expiry = %v, want %v", got, tc.want)
			}
		})
	}
}

// A session in use survives past the idle limit; an unused one does not;
// none survives the absolute limit however busy it is.
func TestMemoryStoreIdleAndAbsoluteLimits(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	now := start
	store := NewSessionStore(Lifetime{Idle: time.Hour, Absolute: 3 * time.Hour})
	store.now = func() time.Time { return now }

	busy, _ := store.Create(ctx, Principal{Subject: "busy"})
	idle, _ := store.Create(ctx, Principal{Subject: "idle"})
	for now = start.Add(50 * time.Minute); now.Before(start.Add(3 * time.Hour)); now = now.Add(50 * time.Minute) {
		if _, ok := store.Get(ctx, busy); !ok {
			t.Fatalf("session in use ended at %v", now.Sub(start))
		}
	}
	if _, ok := store.Get(ctx, idle); ok {
		t.Fatal("session unused for more than the idle limit is still valid")
	}
	now = start.Add(3 * time.Hour)
	if _, ok := store.Get(ctx, busy); ok {
		t.Fatal("session in use outlived the absolute limit")
	}
}

func TestSessionLifetimeDefaultsAndCookie(t *testing.T) {
	if got := SessionLifetime(config.Auth{}); got != (Lifetime{Idle: 12 * time.Hour, Absolute: 7 * 24 * time.Hour}) {
		t.Fatalf("defaults = %+v", got)
	}
	if got := SessionLifetime(config.Auth{SessionTTLMinutes: 60, SessionIdleMinutes: 15}); got != (Lifetime{Idle: 15 * time.Minute, Absolute: time.Hour}) {
		t.Fatalf("configured = %+v", got)
	}
	c := SessionCookie("id", "example.com", true, Lifetime{Absolute: time.Hour})
	if c.MaxAge != 3600 || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Name != SessionCookieName {
		t.Fatalf("cookie = %+v", c)
	}
}
