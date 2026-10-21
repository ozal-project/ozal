package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
)

// migratedSessions opens a real database file with every embedded migration
// applied and returns a session store over it.
func migratedSessions(t *testing.T, path string, ttl time.Duration) *Sessions {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrations, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return NewSessions(db, auth.Lifetime{Absolute: ttl})
}

func TestSessionsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := migratedSessions(t, t.TempDir()+"/state.db", time.Hour)
	principal := auth.Principal{Subject: "sub-1", Email: "ana@example.com", EmailVerified: true, Groups: []string{"ops", "devs"}}

	id, err := store.Create(ctx, principal)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	session, ok := store.Get(ctx, id)
	if !ok {
		t.Fatal("the session was not found")
	}
	if session.Principal.Subject != principal.Subject || session.Principal.Email != principal.Email ||
		!session.Principal.EmailVerified || len(session.Principal.Groups) != 2 {
		t.Fatalf("session = %+v", session)
	}
	if !session.ExpiresAt.After(time.Now()) {
		t.Fatalf("expires at = %s, want the future", session.ExpiresAt)
	}
	// The raw ID must not be stored: only its hash.
	var raw []byte
	if err := store.db.QueryRow(`SELECT id_hash FROM sessions`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if string(raw) == id {
		t.Fatal("the raw session ID reached the database")
	}
	if len(raw) != 32 {
		t.Fatalf("id_hash length = %d, want a SHA-256", len(raw))
	}
}

func TestSessionsSurviveRestart(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.db"

	first := migratedSessions(t, path, time.Hour)
	id, err := first.Create(ctx, auth.Principal{Subject: "sub-1", Email: "ana@example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Close the database: this is the process-restart boundary.
	if err := first.db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := migratedSessions(t, path, time.Hour)
	session, ok := second.Get(ctx, id)
	if !ok {
		t.Fatal("the session did not survive the restart")
	}
	if session.Principal.Subject != "sub-1" {
		t.Fatalf("session = %+v", session)
	}
}

func TestSessionsRejectExpiredRows(t *testing.T) {
	ctx := context.Background()
	store := migratedSessions(t, t.TempDir()+"/state.db", -time.Second) // already expired on creation

	id, err := store.Create(ctx, auth.Principal{Subject: "sub-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := store.Get(ctx, id); ok {
		t.Fatal("an expired session must not be accepted")
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%d rows left, want the expired row evicted on lookup", count)
	}
}

func TestSessionsSweepRemovesExpiredRows(t *testing.T) {
	ctx := context.Background()
	store := migratedSessions(t, t.TempDir()+"/state.db", time.Hour)
	if _, err := store.Create(ctx, auth.Principal{Subject: "fresh"}); err != nil {
		t.Fatal(err)
	}
	// A stale row from an earlier run: the sweep must take it and leave the
	// fresh one alone.
	if _, err := store.db.Exec(`INSERT INTO sessions (id_hash, subject, email, email_verified, groups_json, created_at, expires_at)
		VALUES (x'00', 'old', '', 0, '[]', 0, 1)`); err != nil {
		t.Fatal(err)
	}
	store.Sweep(ctx)
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows after sweep = %d, want 1", count)
	}
}

func TestSessionsCapacityIsEnforced(t *testing.T) {
	ctx := context.Background()
	store := migratedSessions(t, t.TempDir()+"/state.db", time.Hour)
	store.maxSessions = 2

	for i := 0; i < 2; i++ {
		if _, err := store.Create(ctx, auth.Principal{Subject: "sub"}); err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
	}
	if _, err := store.Create(ctx, auth.Principal{Subject: "one-too-many"}); !errors.Is(err, auth.ErrStoreFull) {
		t.Fatalf("Create at capacity: err = %v, want ErrStoreFull", err)
	}
	// An expired session does not consume a slot: Create purges first.
	expired := migratedSessions(t, t.TempDir()+"/expired.db", -time.Second)
	expired.maxSessions = 2
	for i := 0; i < 2; i++ {
		if _, err := expired.Create(ctx, auth.Principal{Subject: "sub"}); err != nil {
			t.Fatalf("expired Create #%d: %v", i, err)
		}
	}
	if _, err := expired.Create(ctx, auth.Principal{Subject: "third"}); err != nil {
		t.Fatalf("Create must succeed when the only rows are expired: %v", err)
	}
}

func TestSessionsDeleteAndUnknownIDs(t *testing.T) {
	ctx := context.Background()
	store := migratedSessions(t, t.TempDir()+"/state.db", time.Hour)
	id, err := store.Create(ctx, auth.Principal{Subject: "sub-1"})
	if err != nil {
		t.Fatal(err)
	}
	store.Delete(ctx, id)
	if _, ok := store.Get(ctx, id); ok {
		t.Fatal("the deleted session was found")
	}
	store.Delete(ctx, "never-existed") // must not panic
	if _, ok := store.Get(ctx, "never-existed"); ok {
		t.Fatal("an unknown ID must not authenticate")
	}
}

func TestSessionsHonorContextCancellation(t *testing.T) {
	store := migratedSessions(t, t.TempDir()+"/state.db", time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Create(ctx, auth.Principal{Subject: "sub"}); err == nil {
		t.Fatal("Create must fail on a cancelled context instead of writing")
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%d rows were written despite the cancelled context", count)
	}
}

func TestSessionsApplyCurrentTTLWithoutExtendingOriginalExpiry(t *testing.T) {
	ctx := context.Background()
	original := migratedSessions(t, t.TempDir()+"/state.db", 8*time.Hour)
	created := time.Unix(1800000000, 0)
	original.now = func() time.Time { return created }
	id, err := original.Create(ctx, auth.Principal{Subject: "sub-1"})
	if err != nil {
		t.Fatal(err)
	}
	shortened := NewSessions(original.db, auth.Lifetime{Absolute: 30 * time.Minute})
	shortened.now = func() time.Time { return created.Add(10 * time.Minute) }
	session, ok := shortened.Get(ctx, id)
	if !ok || !session.ExpiresAt.Equal(created.Add(30*time.Minute)) {
		t.Fatalf("session under shorter TTL = %+v, %v", session, ok)
	}
	// Constructing a candidate backend must not invalidate the installed snapshot.
	if session, ok := original.Get(ctx, id); !ok || !session.ExpiresAt.Equal(created.Add(8*time.Hour)) {
		t.Fatal("candidate changed the installed backend")
	}
	shortened.now = func() time.Time { return created.Add(30 * time.Minute) }
	if _, ok := shortened.Get(ctx, id); ok {
		t.Fatal("old session accepted beyond the current TTL")
	}
	id, err = original.Create(ctx, auth.Principal{Subject: "sub-2"})
	if err != nil {
		t.Fatal(err)
	}
	extended := NewSessions(original.db, auth.Lifetime{Absolute: 24 * time.Hour})
	extended.now = func() time.Time { return created.Add(8 * time.Hour) }
	if _, ok := extended.Get(ctx, id); ok {
		t.Fatal("a longer TTL extended the original session expiry")
	}
}

// Idle limit over the database: use moves last_seen_at (at most once per
// TouchInterval), no use ends the session, and the absolute limit still
// wins over a busy session. Sweep removes idle rows nobody presents again.
func TestSessionsIdleLimitAndTouch(t *testing.T) {
	ctx := context.Background()
	store := migratedSessions(t, t.TempDir()+"/state.db", 3*time.Hour)
	store.lifetime.Idle = time.Hour
	start := time.Unix(1800000000, 0)
	now := start
	store.now = func() time.Time { return now }
	lastSeen := func(id string) int64 {
		t.Helper()
		hash := sha256.Sum256([]byte(id))
		var seen int64
		if err := store.db.QueryRowContext(ctx, `SELECT last_seen_at FROM sessions WHERE id_hash = ?`, hash[:]).Scan(&seen); err != nil {
			t.Fatal(err)
		}
		return seen
	}

	busy, _ := store.Create(ctx, auth.Principal{Subject: "busy"})
	idle, _ := store.Create(ctx, auth.Principal{Subject: "idle"})
	abandoned, _ := store.Create(ctx, auth.Principal{Subject: "abandoned"})

	now = start.Add(30 * time.Second)
	if _, ok := store.Get(ctx, busy); !ok || lastSeen(busy) != start.Unix() {
		t.Fatal("a use within TouchInterval wrote last_seen_at")
	}
	for now = start.Add(50 * time.Minute); now.Before(start.Add(3 * time.Hour)); now = now.Add(50 * time.Minute) {
		session, ok := store.Get(ctx, busy)
		if !ok {
			t.Fatalf("session in use ended at %v", now.Sub(start))
		}
		if lastSeen(busy) != now.Unix() {
			t.Fatal("use did not record last_seen_at")
		}
		if want := now.Add(time.Hour); want.Before(start.Add(3*time.Hour)) && !session.ExpiresAt.Equal(want) {
			t.Fatalf("ExpiresAt = %v, want %v", session.ExpiresAt, want)
		}
	}
	if _, ok := store.Get(ctx, idle); ok {
		t.Fatal("session unused for more than the idle limit is still valid")
	}
	// Sweep inside the absolute limit, so only the idle rule can remove it.
	now = start.Add(2 * time.Hour)
	store.Sweep(ctx)
	hash := sha256.Sum256([]byte(abandoned))
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id_hash = ?`, hash[:]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("sweep kept an idle session: %d, %v", count, err)
	}
	now = start.Add(3 * time.Hour)
	if _, ok := store.Get(ctx, busy); ok {
		t.Fatal("session in use outlived the absolute limit")
	}
}
