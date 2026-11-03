package admin

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/passkey"
	"github.com/ozal-project/ozal/internal/store/sqlite"
)

// visitorsOnly is a people directory that only lists users.
type visitorsOnly []passkey.User

func (v visitorsOnly) Users(context.Context) ([]passkey.User, error) { return v, nil }
func (visitorsOnly) Passkeys(context.Context, string) ([]passkey.Passkey, error) {
	return nil, nil
}
func (visitorsOnly) Invite(context.Context, string, string, []string, bool, time.Duration) (sqlite.Invitation, error) {
	return sqlite.Invitation{}, nil
}
func (visitorsOnly) RevokePasskey(context.Context, string, string) error         { return nil }
func (visitorsOnly) SetDisabled(context.Context, string, bool) error             { return nil }
func (visitorsOnly) SetGroups(context.Context, string, []string, []string) error { return nil }

// The account menu learns the administrator's email and whether that email
// also has an enabled visitor account; a host login has no account to show.
func TestOwnAccountNamesTheAdministratorAndAVisitorWithTheSameEmail(t *testing.T) {
	for _, tt := range []struct {
		name     string
		visitors visitorsOnly
		want     bool
	}{
		{"same email, any case", visitorsOnly{{Email: "Octavio@Example.com"}}, true},
		{"different email", visitorsOnly{{Email: "ana@example.com"}}, false},
		{"disabled visitor", visitorsOnly{{Email: "octavio@example.com", DisabledAt: time.Now()}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := identityHandler(t, WithPeople(tt.visitors))
			token, _ := hostEnrolment(t, h, "bootstrap", "")
			session := decode(t, enrol(t, h, newAuthenticator(t), token, "Octavio", "octavio@example.com"))["token"].(string)
			rec := request(h, http.MethodGet, "/_/api/admin/account", session, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("account = %d %s", rec.Code, rec.Body)
			}
			got := decode(t, rec)
			if got["email"] != "octavio@example.com" || got["name"] != "Octavio" || got["also_visitor"] != tt.want {
				t.Fatalf("account = %v, want also_visitor %v", got, tt.want)
			}
		})
	}
	h, _ := identityHandler(t, WithPeople(visitorsOnly{}))
	if rec := request(h, http.MethodGet, "/_/api/admin/account", "secret", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("admin token account = %d, want 403: it has no administrator account", rec.Code)
	}
}
