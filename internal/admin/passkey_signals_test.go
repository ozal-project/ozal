package admin

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/adminid"
)

type failingSignalIdentities struct {
	identities
	list, retired bool
}

func (s *failingSignalIdentities) Passkeys(ctx context.Context, id string) ([]adminid.Passkey, error) {
	if s.list {
		return nil, errors.New("list unavailable")
	}
	return s.identities.Passkeys(ctx, id)
}

func (s *failingSignalIdentities) CredentialRevoked(ctx context.Context, handle, id []byte) (bool, error) {
	if s.retired {
		return false, errors.New("check unavailable")
	}
	return s.identities.CredentialRevoked(ctx, handle, id)
}

func finishRemoteAttempt(t *testing.T, h *Handler, remote *RemoteLogin, device *virtualAuthenticator) *httptest.ResponseRecorder {
	t.Helper()
	_, challenge := verifier()
	start := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/start", "", map[string]string{"challenge": challenge})
	u, err := url.Parse(decode(t, start)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	query := "?tx=" + u.Query().Get("tx")
	_, begin := atLoginHost(remote, http.MethodPost, remotePath+"/begin"+query, "", map[string]any{})
	begun := decode(t, begin)
	assertion := device.get(t, begun["options"].(map[string]any), loginHostOrigin)
	_, finish := atLoginHost(remote, http.MethodPost, remotePath+"/finish"+query, begun["ceremony"].(string), assertion)
	return finish
}

func TestLoginHostSignalsItsOwnDirectoryAndOmitsFailedReads(t *testing.T) {
	h, remote, _ := loginHostHandler(t, true)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	first, second := newAuthenticator(t), newAuthenticator(t)
	secret, challenge := verifier()
	code, _ := loginHostCeremony(t, h, remote, first, map[string]any{"challenge": challenge, "token": token, "name": "Owner", "email": "owner@example.com"})
	session := redeem(t, h, code, secret)["token"].(string)
	grant, _ := h.operators.authenticate(session)
	accountID := h.operators.account(grant.ID)
	deviceLink, _, err := h.devices.issue(accountID)
	if err != nil {
		t.Fatal(err)
	}
	_, challenge = verifier()
	loginHostCeremony(t, h, remote, second, map[string]any{"challenge": challenge, "token": deviceLink})
	finish := finishRemoteAttempt(t, h, remote, first)
	body := decode(t, finish)
	list, ok := body["accepted"].(map[string]any)
	if finish.Code != http.StatusOK || !ok || list["rp_id"] != "example.com" || list["user_id"] != base64.RawURLEncoding.EncodeToString(first.userHandle) {
		t.Fatalf("accepted list: %d %v", finish.Code, body)
	}
	ids := list["ids"].([]any)
	want := map[string]bool{base64.RawURLEncoding.EncodeToString(first.credentialID): true, base64.RawURLEncoding.EncodeToString(second.credentialID): true}
	if len(ids) != len(want) {
		t.Fatalf("partial list: %v", ids)
	}
	for _, id := range ids {
		if !want[id.(string)] {
			t.Fatalf("another directory's credential: %v", id)
		}
	}
	failure := &failingSignalIdentities{identities: h.identities, list: true}
	h.identities = failure
	finish = finishRemoteAttempt(t, h, remote, first)
	if body := decode(t, finish); finish.Code != http.StatusOK || body["accepted"] != nil {
		t.Fatalf("unreadable list held sign-in or sent a list: %d %v", finish.Code, body)
	}
	failure.list = false
	if err := h.identities.RemovePasskey(context.Background(), accountID, second.credentialID); err != nil {
		t.Fatal(err)
	}
	finish = finishRemoteAttempt(t, h, remote, second)
	gone, ok := decode(t, finish)["unknown_credential"].(map[string]any)
	if finish.Code != http.StatusUnauthorized || !ok || gone["credential_id"] != base64.RawURLEncoding.EncodeToString(second.credentialID) {
		t.Fatalf("retired credential: %d %s", finish.Code, finish.Body)
	}
	failure.retired = true
	finish = finishRemoteAttempt(t, h, remote, second)
	if decode(t, finish)["unknown_credential"] != nil {
		t.Fatal("a failed check named a credential")
	}
	if revoked, err := failure.identities.CredentialRevoked(context.Background(), []byte("another handle"), second.credentialID); err != nil || revoked {
		t.Fatalf("another handle discovered the retired credential: %v %v", revoked, err)
	}
}

func TestHTTPSReauthenticationAllowsRenameAfterProofExpires(t *testing.T) {
	h, remote, _ := loginHostHandler(t, true)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	device := newAuthenticator(t)
	secret, challenge := verifier()
	code, _ := loginHostCeremony(t, h, remote, device, map[string]any{"challenge": challenge, "token": token, "name": "Owner", "email": "owner@example.com"})
	session := redeem(t, h, code, secret)["token"].(string)
	id := ownPasskeys(t, h, session)[0]["id"].(string)
	h.operators.now = func() time.Time { return time.Now().Add(passkeyStepUpMaxAge + time.Minute) }
	path := "/_/api/admin/passkeys/" + id
	if rec := api(h, http.MethodPatch, path, session, map[string]string{"label": "Laptop"}); rec.Code != http.StatusForbidden {
		t.Fatalf("stale rename: %d", rec.Code)
	}
	begin := browserAs(h, "ozal.localhost:9091", "/_/api/admin/step-up/begin", "", session, map[string]any{})
	if begin.Code != http.StatusConflict || decode(t, begin)["code"] != "login_host_required" {
		t.Fatalf("HTTPS proof was sent to localhost: %d %s", begin.Code, begin.Body)
	}
	secret, challenge = verifier()
	code, _ = loginHostCeremony(t, h, remote, device, map[string]any{"challenge": challenge})
	fresh := redeem(t, h, code, secret)["token"].(string)
	if fresh == session {
		t.Fatal("reauthentication reused the stale session")
	}
	if rec := api(h, http.MethodPatch, path, fresh, map[string]string{"label": "Laptop"}); rec.Code != http.StatusOK {
		t.Fatalf("rename after HTTPS reauthentication: %d %s", rec.Code, rec.Body)
	}
}
