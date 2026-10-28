package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ID-015: the in-memory store follows the same rules as the SQLite one.
func TestMemoryHostSessionsFollowTheirCentralSession(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	clock := start
	store := NewSessionStore(Lifetime{Absolute: time.Hour, Idle: 30 * time.Minute})
	store.now = func() time.Time { return clock }
	login := func() (string, string, string) {
		central, _ := store.Create(ctx, Principal{Subject: "sb:ana", Groups: []string{"ops"}, Method: MethodPasskey})
		docs, principal, err := store.CreateLinked(ctx, central, "docs.other.test")
		if err != nil || principal.Subject != "sb:ana" {
			t.Fatalf("CreateLinked = %+v, %v", principal, err)
		}
		wiki, _, _ := store.CreateLinked(ctx, central, "wiki.other.test")
		return central, docs, wiki
	}
	present := func(id string) bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		_, ok := store.sessions[id]
		return ok
	}

	central, docs, _ := login()
	session, ok := store.Get(ctx, docs)
	parent, _ := store.Get(ctx, central)
	if !ok || session.Audience != "docs.other.test" || parent.Audience != "" || session.ExpiresAt.After(parent.ExpiresAt) {
		t.Fatalf("host session = %+v, %v", session, ok)
	}
	for _, parentID := range []string{docs, "nope"} {
		if _, _, err := store.CreateLinked(ctx, parentID, "x.other.test"); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("CreateLinked under %q: %v", parentID, err)
		}
	}
	clock = start.Add(25 * time.Minute)
	store.Get(ctx, docs)
	clock = start.Add(50 * time.Minute)
	if _, ok := store.Get(ctx, central); !ok {
		t.Fatal("the central session idled out while an app was in use")
	}

	clock = start
	central, docs, wiki := login()
	store.mu.Lock()
	delete(store.sessions, central)
	store.mu.Unlock()
	if _, ok := store.Get(ctx, docs); ok {
		t.Fatal("a host session outlived its central session")
	}
	store.Sweep(ctx)
	if present(wiki) {
		t.Fatal("sweep kept an orphaned host session")
	}

	central, docs, wiki = login()
	store.Delete(ctx, central)
	if present(docs) || present(wiki) {
		t.Fatal("deleting the central session left host sessions")
	}
	central, docs, wiki = login()
	store.DeleteLogin(ctx, docs)
	if present(central) || present(docs) || present(wiki) {
		t.Fatal("DeleteLogin from an app left part of the login")
	}
}
