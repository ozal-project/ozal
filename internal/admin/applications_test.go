package admin

import (
	"github.com/switchboard-project/switchboard/internal/config"
	"net/http"
	"strings"
	"testing"
)

func TestApplicationPlanRequiresAuthorityExplicitPrivacyAndFreshConfig(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Listen: "127.0.0.1:8888", Admin: config.Admin{Token: "secret"}, Routes: []config.Route{{Host: "old.localhost", Target: "http://127.0.0.1:3000"}}}}
	handler := New(manager, nil)
	digest := manager.ConfigDigest()
	// The fake digest is fixed, but the real route manager repeats the check
	// inside its serialized mutation, so a change after planning is also refused.
	body := `{"expected":"` + digest + `","access_mode":"public","route":{"host":"new.localhost","target":"http://127.0.0.1:3000"}}`
	if rec := request(handler, http.MethodPost, "/_/api/apps/plan", "", body); rec.Code != 401 {
		t.Fatalf("anonymous plan: %d", rec.Code)
	}
	if rec := request(handler, http.MethodPost, "/_/api/apps/plan", "secret", body); rec.Code != 200 {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{
		strings.Replace(body, `"access_mode":"public",`, "", 1),
		strings.Replace(body, digest, "stale", 1),
		strings.Replace(body, `"public"`, `"private"`, 1),
	} {
		if rec := request(handler, http.MethodPost, "/_/api/apps/plan", "secret", bad); rec.Code < 400 {
			t.Fatalf("unsafe plan accepted: %s", bad)
		}
	}
	if len(manager.added) != 0 {
		t.Fatal("planning mutated routing")
	}
	if rec := request(handler, http.MethodGet, "/_/api/setup", "secret", ""); strings.Contains(rec.Body.String(), `"token"`) {
		t.Fatal("setup returned credentials")
	}
}
