package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ozal-project/ozal/internal/adminid"
)

func enrolledAdmin(t *testing.T) (*Admins, adminid.Account) {
	t.Helper()
	ctx := context.Background()
	db, _ := openTestDB(t)
	migrations, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	admins := NewAdmins(db)
	hash := sha256.Sum256([]byte("bootstrap"))
	if err := admins.IssueEnrolment(ctx, adminid.EnrolmentRequest{Kind: "bootstrap"}, hash[:], time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	account := adminid.Account{ID: "0123456789abcdef0123456789abcdef", Handle: []byte("0123456789abcdef0123456789abcdef"), Email: "owner@example.com", Name: "Owner", Tools: []string{"administrators"}}
	account, err = admins.CompleteEnrolment(ctx, hash[:], account, webauthn.Credential{ID: []byte("first-passkey-credential")}, "Firefox on Linux")
	if err != nil {
		t.Fatal(err)
	}
	return admins, account
}

func TestRemovePasskeyKeepsTheLastOneAndOnlyTouchesTheTarget(t *testing.T) {
	ctx := context.Background()
	admins, account := enrolledAdmin(t)
	first := []byte("first-passkey-credential")
	if err := admins.RemovePasskey(ctx, account.ID, first); !errors.Is(err, adminid.ErrLastPasskey) {
		t.Fatalf("removing the only passkey = %v, want ErrLastPasskey", err)
	}
	second := []byte("second-passkey-credential")
	if err := admins.AddPasskey(ctx, account.ID, webauthn.Credential{ID: second}, ""); err != nil {
		t.Fatal(err)
	}
	if err := admins.RemovePasskey(ctx, "ffffffffffffffffffffffffffffffff", second); !errors.Is(err, adminid.ErrPasskeyNotFound) {
		t.Fatalf("another account removed the passkey: %v", err)
	}
	if err := admins.RemovePasskey(ctx, account.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := admins.RemovePasskey(ctx, account.ID, first); !errors.Is(err, adminid.ErrPasskeyNotFound) {
		t.Fatalf("a revoked passkey was removed twice: %v", err)
	}
	passkeys, err := admins.Passkeys(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(passkeys) != 1 || string(passkeys[0].ID) != string(second) || passkeys[0].Label != "" {
		t.Fatalf("passkeys = %+v", passkeys)
	}
	if _, credentials, err := admins.AccountByHandle(ctx, account.Handle); err != nil || len(credentials) != 1 || string(credentials[0].ID) != string(second) {
		t.Fatalf("sign-in credentials = %v, %v", credentials, err)
	}
}

func TestPasskeysCarryTheirLabel(t *testing.T) {
	admins, account := enrolledAdmin(t)
	passkeys, err := admins.Passkeys(context.Background(), account.ID)
	if err != nil || len(passkeys) != 1 || passkeys[0].Label != "Firefox on Linux" || passkeys[0].CreatedAt.IsZero() {
		t.Fatalf("passkeys = %+v, %v", passkeys, err)
	}
}

func TestAccessRequestsSettingIsOffByDefaultAndSurvivesToggling(t *testing.T) {
	ctx := context.Background()
	admins, _ := enrolledAdmin(t)
	if on, err := admins.AccessRequests(ctx); err != nil || on {
		t.Fatalf("fresh database: on=%v err=%v, want off", on, err)
	}
	for _, want := range []bool{true, false, true} {
		if err := admins.SetAccessRequests(ctx, want); err != nil {
			t.Fatal(err)
		}
		if on, err := admins.AccessRequests(ctx); err != nil || on != want {
			t.Fatalf("after setting %v: on=%v err=%v", want, on, err)
		}
	}
}

// The list carries what the authenticator reported at creation, so the
// console can name a known provider; and a rename touches only an active
// passkey of that same account.
func TestPasskeysCarryTheirProviderAndRenameStaysInTheAccount(t *testing.T) {
	ctx := context.Background()
	admins, account := enrolledAdmin(t)
	bitwarden := []byte{0xd5, 0x48, 0x82, 0x6e, 0x79, 0xb4, 0xdb, 0x40, 0xa3, 0xd8, 0x11, 0x11, 0x6f, 0x7e, 0x83, 0x49}
	second := webauthn.Credential{ID: []byte("bitwarden-passkey"), Authenticator: webauthn.Authenticator{AAGUID: bitwarden}, Flags: webauthn.CredentialFlags{BackupEligible: true}}
	if err := admins.AddPasskey(ctx, account.ID, second, ""); err != nil {
		t.Fatal(err)
	}
	passkeys, err := admins.Passkeys(ctx, account.ID)
	if err != nil || len(passkeys) != 2 {
		t.Fatalf("passkeys = %+v, %v", passkeys, err)
	}
	for _, p := range passkeys {
		if string(p.ID) == "bitwarden-passkey" && (string(p.AAGUID) != string(bitwarden) || !p.Synced) {
			t.Fatalf("provider data lost: %+v", p)
		}
	}
	if err := admins.RenamePasskey(ctx, "ffffffffffffffffffffffffffffffff", second.ID, "stolen"); !errors.Is(err, adminid.ErrPasskeyNotFound) {
		t.Fatalf("another account renamed the passkey: %v", err)
	}
	if err := admins.RenamePasskey(ctx, account.ID, second.ID, "Bitwarden vault"); err != nil {
		t.Fatal(err)
	}
	if err := admins.RemovePasskey(ctx, account.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := admins.RenamePasskey(ctx, account.ID, second.ID, "again"); !errors.Is(err, adminid.ErrPasskeyNotFound) {
		t.Fatalf("a revoked passkey was renamed: %v", err)
	}
}
