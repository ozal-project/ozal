package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCreateAndGet(t *testing.T) {
	store := NewSessionStore(time.Hour)

	id, err := store.Create(Principal{Email: "user@example.com", Subject: "sub-123"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	sess, ok := store.Get(id)
	if !ok {
		t.Fatal("expected session to be found")
	}
	if sess.Principal.Email != "user@example.com" || sess.Principal.Subject != "sub-123" {
		t.Errorf("unexpected session contents: %+v", sess)
	}
}

func TestGetUnknownID(t *testing.T) {
	store := NewSessionStore(time.Hour)
	if _, ok := store.Get("does-not-exist"); ok {
		t.Fatal("expected unknown session id to not be found")
	}
}

func TestExpiredSessionIsEvicted(t *testing.T) {
	store := NewSessionStore(-time.Second) // already expired on creation
	id, err := store.Create(Principal{Email: "user@example.com", Subject: "sub-123"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, ok := store.Get(id); ok {
		t.Fatal("expected an expired session to not be found")
	}
}

func TestDelete(t *testing.T) {
	store := NewSessionStore(time.Hour)
	id, _ := store.Create(Principal{Email: "user@example.com", Subject: "sub-123"})

	store.Delete(id)

	if _, ok := store.Get(id); ok {
		t.Fatal("expected session to be gone after Delete")
	}
}

func TestSessionIDsAreUnique(t *testing.T) {
	store := NewSessionStore(time.Hour)
	id1, _ := store.Create(Principal{Email: "a@example.com", Subject: "a"})
	id2, _ := store.Create(Principal{Email: "b@example.com", Subject: "b"})
	if id1 == id2 {
		t.Fatal("expected distinct session ids")
	}
}

func TestCreateFailsAtCapacity(t *testing.T) {
	store := NewSessionStore(time.Hour)
	for i := 0; i < maxSessions; i++ {
		if _, err := store.Create(Principal{Email: "user@example.com", Subject: "sub"}); err != nil {
			t.Fatalf("Create #%d: unexpected error before reaching capacity: %v", i, err)
		}
	}

	if _, err := store.Create(Principal{Email: "one-too-many@example.com", Subject: "sub"}); !errors.Is(err, ErrStoreFull) {
		t.Fatalf("Create at capacity: err = %v, want ErrStoreFull", err)
	}
}

func TestStartCleanupSweepsExpiredSessions(t *testing.T) {
	store := NewSessionStore(-time.Second) // already expired on creation
	id, err := store.Create(Principal{Email: "user@example.com", Subject: "sub-123"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.StartCleanup(ctx, 10*time.Millisecond)

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
