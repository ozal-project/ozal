package sqlite

import (
	"context"
	"errors"
	"github.com/ozal-project/ozal/internal/auth"
	"slices"
	"testing"
	"time"
)

func TestMembershipUpdateEndsOnlyNativeSessionsAndRejectsStaleEdits(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	migrations, _ := LoadMigrations(Migrations())
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	identity := NewIdentity(db)
	invitation, err := identity.Invite(ctx, "ana@example.test", "", []string{"ops"}, false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewSessions(db, auth.Lifetime{Absolute: time.Hour})
	native, _ := sessions.Create(ctx, auth.Principal{Subject: invitation.User.Subject(), Method: auth.MethodPasskey, Groups: []string{"ops"}})
	external, _ := sessions.Create(ctx, auth.Principal{Subject: auth.OIDCSubject("https://idp.example", "ana"), Method: auth.MethodOIDC, Groups: []string{"ops"}})
	if err := identity.SetGroups(ctx, "ANA@example.test", []string{"ops"}, []string{"staff", "staff"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := sessions.Get(ctx, native); ok {
		t.Fatal("old native session survived membership removal")
	}
	if _, ok := sessions.Get(ctx, external); !ok {
		t.Fatal("independent external session was revoked")
	}
	if err := identity.SetGroups(ctx, "ana@example.test", []string{"ops"}, []string{"owner"}); !errors.Is(err, ErrMembershipChanged) {
		t.Fatalf("stale edit: %v", err)
	}
	users, _ := identity.Users(ctx)
	if !slices.Equal(users[0].Groups, []string{"staff"}) {
		t.Fatalf("groups: %v", users[0].Groups)
	}
	// Re-inviting never changes existing membership.
	if _, err := identity.Invite(ctx, "ana@example.test", "", []string{"owner"}, false, time.Hour); err != nil {
		t.Fatal(err)
	}
	users, _ = identity.Users(ctx)
	if !slices.Equal(users[0].Groups, []string{"staff"}) {
		t.Fatal("invite edited membership")
	}
}

func TestSavingUnchangedMembershipStillHonorsSessionTermination(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	migrations, _ := LoadMigrations(Migrations())
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	identity := NewIdentity(db)
	invitation, err := identity.Invite(ctx, "ana@example.test", "", []string{"ops"}, false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewSessions(db, auth.Lifetime{Absolute: time.Hour})
	id, err := sessions.Create(ctx, auth.Principal{Subject: invitation.User.Subject(), Method: auth.MethodPasskey})
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.SetGroups(ctx, "ana@example.test", []string{"ops"}, []string{"ops"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := sessions.Get(ctx, id); ok {
		t.Fatal("confirmed session termination did not happen on unchanged membership")
	}
}
