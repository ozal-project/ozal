package passkey_test

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"testing"

	"github.com/ozal-project/ozal/internal/passkey"
	"github.com/ozal-project/ozal/internal/store/sqlite"
)

// flakyDirectory fails the reads behind the Signal API on demand.
type flakyDirectory struct {
	*sqlite.Identity
	failList, failRevokedCheck bool
}

func (d *flakyDirectory) ActivePasskeys(ctx context.Context, userID string) ([]passkey.Passkey, error) {
	if d.failList {
		return nil, errors.New("storage unavailable")
	}
	return d.Identity.ActivePasskeys(ctx, userID)
}

func (d *flakyDirectory) CredentialRevoked(ctx context.Context, handle, id []byte) (bool, error) {
	if d.failRevokedCheck {
		return false, errors.New("storage unavailable")
	}
	return d.Identity.CredentialRevoked(ctx, handle, id)
}

func hexToB64(t *testing.T, id string) string {
	t.Helper()
	raw, err := hex.DecodeString(id)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func acceptedIDs(t *testing.T, body map[string]any) (rpID, userID string, ids []string) {
	t.Helper()
	list, ok := body["accepted"].(map[string]any)
	if !ok {
		return "", "", nil
	}
	for _, id := range list["ids"].([]any) {
		ids = append(ids, id.(string))
	}
	sort.Strings(ids)
	return list["rp_id"].(string), list["user_id"].(string), ids
}

// A sign-in carries the account's complete passkey list for this relying
// party, keyed by the account's user handle; revoking one answers with the
// passkeys left, read in the same transaction, though the session ends.
func TestSignInAndRevokeCarryTheCompleteAcceptedList(t *testing.T) {
	h := newHarness(t)
	phone, laptop := newAuthenticator(t), newAuthenticator(t)
	ana := h.browser()
	if code := ana.enrol(t, phone, h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatal("enrol")
	}
	rec, options := ana.post(t, "/_/auth/devices/add/begin", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("add begin = %d", rec.Code)
	}
	if rec, _ := ana.post(t, "/_/auth/devices/add/finish", laptop.create(t, options, loginOrigin)); rec.Code != http.StatusOK {
		t.Fatalf("add finish = %d", rec.Code)
	}
	var want []string
	var laptopID string
	for _, d := range passkeysOf(t, ana) {
		want = append(want, hexToB64(t, d["id"].(string)))
		if !d["current"].(bool) {
			laptopID = d["id"].(string)
		}
	}
	sort.Strings(want)

	code, body := h.browser().login(t, phone, "", loginOrigin)
	rpID, userID, ids := acceptedIDs(t, body)
	if code != http.StatusOK || rpID != "example.test" || userID == "" || len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("sign-in accepted list: %d %q %q %v, want %v", code, rpID, userID, ids, want)
	}
	user, _, err := h.identity.UserByHandle(context.Background(), mustB64(t, userID))
	if err != nil || user.Email != "ana@example.com" {
		t.Fatalf("user_id is not ana's handle: %v %v", user, err)
	}

	rec, revoked := ana.post(t, "/_/auth/devices/revoke", map[string]string{"id": laptopID})
	_, revUser, left := acceptedIDs(t, revoked)
	if rec.Code != http.StatusOK || revoked["signed_out"] != true || revUser != userID || len(left) != 1 || left[0] == hexToB64(t, laptopID) {
		t.Fatalf("revoke = %d %v", rec.Code, revoked)
	}
	if _, ok := ana.signedIn(); ok {
		t.Fatal("the revocation did not end the session")
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A list that cannot be read is never replaced by an empty or partial one:
// the sign-in still succeeds, without a list.
func TestSignInWithoutAReadableListSendsNoList(t *testing.T) {
	var dir *flakyDirectory
	h := buildHarness(t, func(id *sqlite.Identity) passkey.Directory { dir = &flakyDirectory{Identity: id}; return dir }, appHost)
	device := newAuthenticator(t)
	if code := h.browser().enrol(t, device, h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatal("enrol")
	}
	dir.failList = true
	code, body := h.browser().login(t, device, "", loginOrigin)
	if _, has := body["accepted"]; code != http.StatusOK || has {
		t.Fatalf("sign-in with an unreadable list = %d %v", code, body)
	}
}

// Only a passkey this directory held and revoked is named as retired. A
// passkey it never held (such as an administrator's, from the other
// directory), a disabled account's active passkey, or a failed check says
// nothing, so a working passkey is never hidden.
func TestOnlyARevokedPasskeyIsNamedUnknown(t *testing.T) {
	var dir *flakyDirectory
	h := buildHarness(t, func(id *sqlite.Identity) passkey.Directory { dir = &flakyDirectory{Identity: id}; return dir }, appHost)
	phone, laptop := newAuthenticator(t), newAuthenticator(t)
	ana := h.browser()
	ana.enrol(t, phone, h.invite(t, "ana@example.com", false))
	_, options := ana.post(t, "/_/auth/devices/add/begin", map[string]any{})
	ana.post(t, "/_/auth/devices/add/finish", laptop.create(t, options, loginOrigin))
	var laptopID string
	for _, d := range passkeysOf(t, ana) {
		if !d["current"].(bool) {
			laptopID = d["id"].(string)
		}
	}
	if rec, _ := ana.post(t, "/_/auth/devices/revoke", map[string]string{"id": laptopID}); rec.Code != http.StatusOK {
		t.Fatal("revoke")
	}

	code, body := h.browser().login(t, laptop, "", loginOrigin)
	unknown, _ := body["unknown_credential"].(map[string]any)
	if code != http.StatusUnauthorized || unknown["rp_id"] != "example.test" || unknown["credential_id"] != hexToB64(t, laptopID) {
		t.Fatalf("revoked passkey: %d %v", code, body)
	}

	dir.failRevokedCheck = true
	if _, body := h.browser().login(t, laptop, "", loginOrigin); body["unknown_credential"] != nil {
		t.Fatalf("a failed check named the passkey: %v", body)
	}
	dir.failRevokedCheck = false

	// Knowing a revoked credential ID is not enough: with another account's
	// user handle the answer is the same as for a passkey never held.
	if revoked, err := h.identity.CredentialRevoked(context.Background(), []byte("not-anas-handle-00000000000000000"), mustHex(t, laptopID)); err != nil || revoked {
		t.Fatalf("a revoked passkey was confirmed under another handle: %v %v", revoked, err)
	}

	// A passkey from another directory (here, another site's records).
	other := newHarness(t)
	stranger := newAuthenticator(t)
	other.browser().enrol(t, stranger, other.invite(t, "bo@example.com", false))
	if code, body := h.browser().login(t, stranger, "", loginOrigin); code != http.StatusUnauthorized || body["unknown_credential"] != nil {
		t.Fatalf("another directory's passkey: %d %v", code, body)
	}

	// A disabled account's passkey is still active: nothing is named.
	if err := h.identity.SetDisabled(context.Background(), "ana@example.com", true); err != nil {
		t.Fatal(err)
	}
	if code, body := h.browser().login(t, phone, "", loginOrigin); code == http.StatusOK || body["unknown_credential"] != nil {
		t.Fatalf("a disabled account's passkey: %d %v", code, body)
	}
}
