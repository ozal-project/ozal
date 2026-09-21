package auth

import (
	"testing"
	"time"
)

func TestCreateAndGet(t *testing.T) {
	store := NewSessionStore(time.Hour)

	id, err := store.Create("user@example.com", "sub-123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	sess, ok := store.Get(id)
	if !ok {
		t.Fatal("expected session to be found")
	}
	if sess.Email != "user@example.com" || sess.Subject != "sub-123" {
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
	id, err := store.Create("user@example.com", "sub-123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, ok := store.Get(id); ok {
		t.Fatal("expected an expired session to not be found")
	}
}

func TestDelete(t *testing.T) {
	store := NewSessionStore(time.Hour)
	id, _ := store.Create("user@example.com", "sub-123")

	store.Delete(id)

	if _, ok := store.Get(id); ok {
		t.Fatal("expected session to be gone after Delete")
	}
}

func TestSessionIDsAreUnique(t *testing.T) {
	store := NewSessionStore(time.Hour)
	id1, _ := store.Create("a@example.com", "a")
	id2, _ := store.Create("b@example.com", "b")
	if id1 == id2 {
		t.Fatal("expected distinct session ids")
	}
}
