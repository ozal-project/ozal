package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/ozal-project/ozal/internal/auth"
)

// Fail just the optional snapshot on an otherwise real SQLite transaction.
// Security writes and their commit still go through the real driver.
type snapshotFailureConnector struct {
	base driver.Driver
	dsn  string
}

func (c snapshotFailureConnector) Driver() driver.Driver { return c.base }
func (c snapshotFailureConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return snapshotFailureConn{conn}, nil
}

type snapshotFailureConn struct{ driver.Conn }

func (c snapshotFailureConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(query, "SELECT credential_id FROM passkeys WHERE user_id") {
		return nil, errors.New("snapshot unavailable")
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func TestRevocationCommitsWithoutAnOptionalSnapshot(t *testing.T) {
	ctx := context.Background()
	db, path := openTestDB(t)
	migrations, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	identity := NewIdentity(db)
	invite, err := identity.Invite(ctx, "ana@example.test", "", nil, false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first-credential-0000", "other-credential-0000"} {
		if err := identity.AddPasskey(ctx, invite.User.ID, webauthn.Credential{ID: []byte(id)}, ""); err != nil {
			t.Fatal(err)
		}
	}
	sessions := NewSessions(db, auth.Lifetime{Absolute: time.Hour})
	sessionID, err := sessions.Create(ctx, auth.Principal{Subject: invite.User.Subject()})
	if err != nil {
		t.Fatal(err)
	}
	faultyDB := sql.OpenDB(snapshotFailureConnector{base: db.Driver(), dsn: "file:" + path})
	t.Cleanup(func() { faultyDB.Close() })
	remaining, err := NewIdentity(faultyDB).RevokeOwnPasskey(ctx, invite.User.ID, []byte("first-credential-0000"))
	if err != nil || remaining != nil {
		t.Fatalf("revocation with failed snapshot: %v %v", remaining, err)
	}
	keys, err := identity.ActivePasskeys(ctx, invite.User.ID)
	if err != nil || len(keys) != 1 || string(keys[0].Credential.ID) != "other-credential-0000" {
		t.Fatalf("active passkeys after revocation: %v %v", keys, err)
	}
	if _, ok := sessions.Get(ctx, sessionID); ok {
		t.Fatal("a failed snapshot preserved the session")
	}
}
