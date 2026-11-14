package admin

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// An administrator's passkey list says where each one lives only when the
// provider is known, and the owner can name their own passkeys. Renaming
// needs a session opened with a passkey, a fresh passkey proof, and a
// passkey of that same account: the bearer alone is not enough.
func TestAdministratorNamesOnlyTheirOwnPasskeyWithAFreshProof(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	invitation, _ := invite(t, h, owner, "ana@example.com", "people")
	ana := decode(t, enrol(t, h, newAuthenticator(t), invitation, "Ana", "ana@example.com"))["token"].(string)

	own := ownPasskeys(t, h, owner)
	if len(own) != 1 || own[0]["provider_known"] != false || own[0]["provider"] != "Security key or device passkey" || own[0]["synced"] != false {
		t.Fatalf("owner's passkeys = %v", own)
	}
	ownID := own[0]["id"].(string)
	anaID := ownPasskeys(t, h, ana)[0]["id"].(string)
	path := "/_/api/admin/passkeys/"

	if rec := api(h, http.MethodPatch, path+ownID, owner, map[string]string{"label": "  YubiKey  "}); rec.Code != http.StatusOK || decode(t, rec)["label"] != "YubiKey" {
		t.Fatalf("rename own = %d %s", rec.Code, rec.Body)
	}
	if got := ownPasskeys(t, h, owner)[0]["label"]; got != "YubiKey" {
		t.Fatalf("label after rename = %v", got)
	}
	// Another administrator's passkey is not found, and stays as it was.
	if rec := api(h, http.MethodPatch, path+anaID, owner, map[string]string{"label": "mine now"}); rec.Code != http.StatusNotFound {
		t.Fatalf("renaming another account's passkey = %d %s", rec.Code, rec.Body)
	}
	if got := ownPasskeys(t, h, ana)[0]["label"]; got != nil {
		t.Fatalf("another account's passkey was renamed: %v", got)
	}
	// The admin token is not an administrator with passkeys.
	if rec := api(h, http.MethodPatch, path+ownID, "secret", map[string]string{"label": "x"}); rec.Code != http.StatusForbidden {
		t.Fatalf("rename with the admin token = %d", rec.Code)
	}
	for _, bad := range []string{"", "   ", "tab\there", strings.Repeat("x", 61)} {
		if rec := api(h, http.MethodPatch, path+ownID, owner, map[string]string{"label": bad}); rec.Code != http.StatusBadRequest {
			t.Errorf("label %q = %d, want 400", bad, rec.Code)
		}
	}
	// A passkey proof older than five minutes asks for a new one.
	h.operators.now = func() time.Time { return time.Now().Add(passkeyStepUpMaxAge + time.Minute) }
	stale := api(h, http.MethodPatch, path+ownID, owner, map[string]string{"label": "Later"})
	h.operators.now = time.Now
	if stale.Code != http.StatusForbidden || !strings.Contains(stale.Body.String(), "passkey_required") {
		t.Fatalf("a stale proof renamed a passkey: %d %s", stale.Code, stale.Body)
	}
	if got := ownPasskeys(t, h, owner)[0]["label"]; got != "YubiKey" {
		t.Fatalf("a refused rename changed the label to %v", got)
	}
}

func ownPasskeys(t *testing.T, h *Handler, session string) []map[string]any {
	t.Helper()
	rec := api(h, http.MethodGet, "/_/api/admin/passkeys", session, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list passkeys = %d %s", rec.Code, rec.Body)
	}
	var out []map[string]any
	for _, p := range decode(t, rec)["passkeys"].([]any) {
		out = append(out, p.(map[string]any))
	}
	return out
}
