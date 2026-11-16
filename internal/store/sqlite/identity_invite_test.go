package sqlite

import (
	"context"
	"testing"
	"time"
)

// The store refuses an address without a real domain itself, so the offline
// break-glass path cannot create such an account either.
func TestInviteRefusesAnAddressWithoutADomain(t *testing.T) {
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
	for _, bad := range []string{"tu@correo", "ana", "Ana <ana@example.com>"} {
		if _, err := identity.Invite(ctx, bad, "", []string{"x"}, false, time.Hour); err == nil {
			t.Errorf("%q was invited", bad)
		}
	}
	if users, err := identity.Users(ctx); err != nil || len(users) != 0 {
		t.Fatalf("users after refusals = %v, %v", users, err)
	}
	if _, err := identity.Invite(ctx, " Ana@Example.COM ", "", nil, false, time.Hour); err != nil {
		t.Fatalf("a real address with spaces and capitals was refused: %v", err)
	}
}
