package tunnel

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	return NewRegistry(db)
}

func TestRegistryEnrollLookupRevokeReenroll(t *testing.T) {
	ctx := context.Background()
	r := newTestRegistry(t)
	a, b := FingerprintOf([]byte("cert-a")), FingerprintOf([]byte("cert-b"))

	if err := r.Enroll(ctx, "portfolio", a); err != nil {
		t.Fatal(err)
	}
	if name, ok, err := r.Lookup(ctx, a); err != nil || !ok || name != "portfolio" {
		t.Fatalf("lookup = %q %v %v", name, ok, err)
	}
	if _, ok, _ := r.Lookup(ctx, b); ok {
		t.Fatal("unenrolled certificate was authorized")
	}
	if err := r.Enroll(ctx, "portfolio", b); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := r.Enroll(ctx, "other", a); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("duplicate certificate: %v", err)
	}

	if err := r.Revoke(ctx, "portfolio"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.Lookup(ctx, a); ok {
		t.Fatal("revoked certificate still authorized")
	}
	if err := r.Revoke(ctx, "portfolio"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke: %v", err)
	}
	// A revoked name and certificate may be enrolled again; history is kept.
	if err := r.Enroll(ctx, "portfolio", b); err != nil {
		t.Fatal(err)
	}
	if err := r.Enroll(ctx, "again", a); err != nil {
		t.Fatalf("revoked certificate cannot be reused under a new name: %v", err)
	}
	records, err := r.List(ctx)
	if err != nil || len(records) != 3 {
		t.Fatalf("list = %d records, %v", len(records), err)
	}
	if records[0].RevokedAt.IsZero() || !records[1].RevokedAt.IsZero() {
		t.Fatalf("revocation state wrong: %+v", records)
	}
}

func TestRegistryRejectsBadNames(t *testing.T) {
	r := newTestRegistry(t)
	for _, name := range []string{"", "Upper", "has space", "-lead", "trail-", "a/b", "x'; DROP TABLE connectors;--"} {
		if err := r.Enroll(context.Background(), name, FingerprintOf([]byte(name))); !errors.Is(err, ErrInvalidName) {
			t.Errorf("name %q: got %v, want ErrInvalidName", name, err)
		}
	}
}
