package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/switchboard-project/switchboard/internal/auth"
	"testing"
	"time"
)

func TestOwnedSessionsCannotBeListedOrRevokedByAnotherSubject(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	migrations, _ := LoadMigrations(Migrations())
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	sessions := NewSessions(db, auth.Lifetime{Absolute: time.Hour})
	ana, err := sessions.Create(ctx, auth.Principal{Subject: "sb:ana", Method: auth.MethodPasskey})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := sessions.Create(ctx, auth.Principal{Subject: "sb:bob", Method: auth.MethodPasskey})
	if err != nil {
		t.Fatal(err)
	}
	own, err := sessions.ListOwned(ctx, "sb:ana")
	if err != nil || len(own) != 1 || own[0].ID == ana {
		t.Fatalf("unsafe own listing: %+v %v", own, err)
	}
	sum := sha256.Sum256([]byte(ana))
	if own[0].ID != hex.EncodeToString(sum[:]) {
		t.Fatal("handle is not a non-bearer digest")
	}
	if err := sessions.RevokeOwned(ctx, "sb:bob", own[0].ID, false); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatal("revoked somebody else's session")
	}
	if err := sessions.RevokeOwned(ctx, "sb:ana", "", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := sessions.Get(ctx, ana); ok {
		t.Fatal("revoked session works")
	}
	if _, ok := sessions.Get(ctx, bob); !ok {
		t.Fatal("another person's session ended")
	}
}

func TestIdentityNamespaceMigrationForcesOnlyExternalReauthentication(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	migrations, _ := LoadMigrations(Migrations())
	if _, err := Migrate(ctx, db, migrations[:11]); err != nil {
		t.Fatal(err)
	}
	sessions := NewSessions(db, auth.Lifetime{Absolute: time.Hour})
	native, _ := sessions.Create(ctx, auth.Principal{Subject: "sb:ana", Method: auth.MethodPasskey})
	legacy, _ := sessions.Create(ctx, auth.Principal{Subject: "oidc:crafted.legacy", Method: auth.MethodOIDC})
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	if _, ok := sessions.Get(ctx, legacy); ok {
		t.Fatal("legacy external identity survived migration")
	}
	if _, ok := sessions.Get(ctx, native); !ok {
		t.Fatal("migration ended a native session")
	}
}
