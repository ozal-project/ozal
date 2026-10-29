package admin

import (
	"context"
	"github.com/ozal-project/ozal/internal/events"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGroupChangesRequireAuthorityFreshnessAndDurableIntent(t *testing.T) {
	audit := &recordingMutationAudit{}
	handler, identity, recorder := peopleHandler(t, WithAdminMutationAudit(audit, true))
	_, err := identity.Invite(context.Background(), "ana@example.com", "", []string{"ops"}, false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	path := "/_/api/people/ana@example.com/groups"
	body := `{"expected":["ops"],"groups":["staff"]}`
	if rec := request(handler, http.MethodPut, path, "", body); rec.Code != 401 {
		t.Fatalf("no authority: %d", rec.Code)
	}
	if rec := request(handler, http.MethodPut, path, "secret", `{"groups":[]}`); rec.Code != 400 {
		t.Fatalf("missing expected: %d", rec.Code)
	}
	if rec := request(handler, http.MethodPut, path, "secret", body); rec.Code != 200 {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodPut, path, "secret", body); rec.Code != 409 {
		t.Fatalf("stale: %d", rec.Code)
	}
	if !recorder.Has(events.UserGroupsChanged) || !hasMutationCode(audit.events, "user.groups") {
		t.Fatal("missing change event or audit intent")
	}
	if rec := request(handler, http.MethodGet, "/_/api/groups", "secret", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "staff") {
		t.Fatalf("catalog: %d %s", rec.Code, rec.Body)
	}
	audit.err = context.Canceled
	if rec := request(handler, http.MethodPut, path, "secret", `{"expected":["staff"],"groups":["owner"]}`); rec.Code != 503 {
		t.Fatalf("required audit failure: %d", rec.Code)
	}
	users, _ := identity.Users(context.Background())
	if users[0].Groups[0] != "staff" {
		t.Fatal("audit failure changed groups")
	}
}
