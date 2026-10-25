package sqlite

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

func TestFirstSeenPasskeyIsConsumedOnceAcrossConcurrentLogins(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	migrations, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	identity := NewIdentity(db)
	invitation, err := identity.Invite(ctx, "ana@example.test", "", nil, false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	credential := webauthn.Credential{ID: []byte("new-passkey-credential-id")}
	if err := identity.AddPasskey(ctx, invitation.User.ID, credential); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	firstSeen := make([]bool, 2)
	errs := make([]error, 2)
	for i := range firstSeen {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			firstSeen[i], errs[i] = identity.RecordLogin(ctx, credential)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("RecordLogin: %v", err)
		}
	}
	if firstSeen[0] == firstSeen[1] {
		t.Fatalf("concurrent logins first-seen results = %v; exactly one must consume the marker", firstSeen)
	}
}

func TestFirstSeenMigrationLeavesExistingCredentialsKnownOld(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	migrations, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, migrations[:len(migrations)-1]); err != nil {
		t.Fatal(err)
	}
	identity := NewIdentity(db)
	invitation, err := identity.Invite(ctx, "old@example.test", "", nil, false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	old := webauthn.Credential{ID: []byte("existing-passkey-credential")}
	encoded, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO passkeys (credential_id, user_id, credential_json, backup_eligible, created_at) VALUES (?, ?, ?, 0, ?)`,
		old.ID, invitation.User.ID, string(encoded), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	if firstSeen, err := identity.RecordLogin(ctx, old); err != nil || firstSeen {
		t.Fatalf("existing passkey firstSeen=%t err=%v; migration must not relabel it new", firstSeen, err)
	}
	newCredential := webauthn.Credential{ID: []byte("new-passkey-credential")}
	if err := identity.AddPasskey(ctx, invitation.User.ID, newCredential); err != nil {
		t.Fatal(err)
	}
	if firstSeen, err := identity.RecordLogin(ctx, newCredential); err != nil || !firstSeen {
		t.Fatalf("new passkey firstSeen=%t err=%v; want true", firstSeen, err)
	}
}
