package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/auth"
)

// ID-015: host sessions copy the central principal, live only on their
// audience and only while the central session does, and end with it.
func TestHostSessionsFollowTheirCentralSession(t *testing.T) {
	ctx := context.Background()
	store := migratedSessions(t, t.TempDir()+"/state.db", time.Hour)
	db := store.db
	store.lifetime.Idle = 30 * time.Minute
	start := time.Now()
	clock := start
	store.now = func() time.Time { return clock }
	rows := func(id string) int {
		hash := sha256.Sum256([]byte(id))
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id_hash = ?`, hash[:]).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	login := func() (string, string, string) {
		central, err := store.Create(ctx, auth.Principal{Subject: "sb:ana", Email: "ana@example.test", Groups: []string{"ops"}, Method: auth.MethodPasskey, AuthTime: clock})
		if err != nil {
			t.Fatal(err)
		}
		docs, principal, err := store.CreateLinked(ctx, central, "docs.other.test")
		if err != nil || principal.Subject != "sb:ana" {
			t.Fatalf("CreateLinked = %+v, %v", principal, err)
		}
		wiki, _, err := store.CreateLinked(ctx, central, "wiki.other.test")
		if err != nil {
			t.Fatal(err)
		}
		return central, docs, wiki
	}

	central, docs, _ := login()
	session, ok := store.Get(ctx, docs)
	parent, _ := store.Get(ctx, central)
	if !ok || session.Audience != "docs.other.test" || session.Principal.Email != "ana@example.test" || len(session.Principal.Groups) != 1 || parent.Audience != "" {
		t.Fatalf("host session = %+v, %v; central audience %q", session, ok, parent.Audience)
	}
	if session.ExpiresAt.After(parent.ExpiresAt) {
		t.Fatal("a host session outlives its central session")
	}
	for name, parentID := range map[string]string{"host session": docs, "unknown": "nope"} {
		if _, _, err := store.CreateLinked(ctx, parentID, "x.other.test"); !errors.Is(err, auth.ErrSessionNotFound) {
			t.Fatalf("CreateLinked under %s: %v", name, err)
		}
	}
	if _, _, err := store.CreateLinked(ctx, central, ""); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("CreateLinked without an audience: %v", err)
	}

	// Using an app keeps the login alive: 25 + 25 minutes is past the
	// central session's idle limit unless the app's use counts.
	clock = start.Add(25 * time.Minute)
	if _, ok := store.Get(ctx, docs); !ok {
		t.Fatal("host session expired early")
	}
	clock = start.Add(50 * time.Minute)
	if _, ok := store.Get(ctx, central); !ok {
		t.Fatal("the central session idled out while an app was in use")
	}

	// The parent check stands on its own: a central row removed by any
	// path invalidates its host sessions even before they are swept.
	clock = start
	central, docs, wiki := login()
	centralHash := sha256.Sum256([]byte(central))
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, centralHash[:]); err != nil {
		t.Fatal(err)
	}
	if rows(wiki) != 1 {
		t.Fatal("setup: the raw delete cascaded")
	}
	if _, ok := store.Get(ctx, docs); ok {
		t.Fatal("a host session outlived its central session's row")
	}
	store.Sweep(ctx)
	if rows(wiki) != 0 {
		t.Fatal("sweep kept an orphaned host session")
	}

	// Delete of the central session removes its host sessions' rows.
	central, docs, wiki = login()
	store.Delete(ctx, central)
	if rows(docs)+rows(wiki) != 0 {
		t.Fatal("deleting the central session left host session rows")
	}

	// DeleteLogin from an app ends the login and the sibling app.
	central, docs, wiki = login()
	store.DeleteLogin(ctx, docs)
	if rows(central)+rows(docs)+rows(wiki) != 0 {
		t.Fatal("DeleteLogin from an app left part of the login")
	}

	// The person's session list shows logins, not their app sessions, and
	// revoking a login revokes its app sessions.
	if err := store.RevokeOwned(ctx, "sb:ana", "", true); err != nil {
		t.Fatal(err)
	}
	central, docs, wiki = login()
	listed, err := store.ListOwned(ctx, "sb:ana")
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListOwned = %+v, %v", listed, err)
	}
	if err := store.RevokeOwned(ctx, "sb:ana", hex.EncodeToString(centralHash[:]), false); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("revoking a deleted login: %v", err)
	}
	if err := store.RevokeOwned(ctx, "sb:ana", listed[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if rows(central)+rows(docs)+rows(wiki) != 0 {
		t.Fatal("revoking a login left its app sessions")
	}
}
