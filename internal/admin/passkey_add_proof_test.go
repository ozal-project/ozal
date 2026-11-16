package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAddingAdministratorAccessRequiresFreshProof(t *testing.T) {
	for _, path := range []string{"/_/api/admin/passkeys/begin", "/_/api/admin/devices"} {
		for _, age := range []time.Duration{passkeyStepUpMaxAge + time.Minute, -2 * time.Minute} {
			t.Run(path+"/"+age.String(), func(t *testing.T) {
				h, _ := identityHandler(t)
				now := time.Now()
				h.operators.now = func() time.Time { return now }
				session := ownerSession(t, h)
				now = now.Add(age)
				// The session remains valid; only its proof is unacceptable.
				if len(ownPasskeys(t, h, session)) != 1 {
					t.Fatal("expected an authenticated account with one passkey")
				}
				rec := browserAs(h, "ozal.localhost:9091", path, "", session, map[string]any{})
				if rec.Code != http.StatusForbidden || decode(t, rec)["code"] != "passkey_required" {
					t.Fatalf("access without a recent proof = %d; want 403 passkey_required", rec.Code)
				}
				if len(h.ceremonies.pending) != 0 || len(h.devices.links) != 0 {
					t.Fatal("refusal created a ceremony or device link")
				}
				if len(ownPasskeys(t, h, session)) != 1 {
					t.Fatal("refusal changed the account's credentials")
				}
			})
		}
	}
}

func TestAdministratorAddFinishRequiresFreshProofWithoutConsumingCeremony(t *testing.T) {
	h, _ := identityHandler(t)
	now := time.Now()
	h.operators.now = func() time.Time { return now }
	first := newAuthenticator(t)
	invitation, _ := hostEnrolment(t, h, "bootstrap", "")
	session := decode(t, enrol(t, h, first, invitation, "Owner", "owner@example.com"))["token"].(string)
	begin := browserAs(h, "ozal.localhost:9091", "/_/api/admin/passkeys/begin", "", session, map[string]any{})
	if begin.Code != http.StatusOK {
		t.Fatalf("fresh begin = %d", begin.Code)
	}
	started := decode(t, begin)
	ceremony := started["ceremony"].(string)
	second := newAuthenticator(t)
	credential := second.create(t, started["options"].(map[string]any), consoleOrigin)
	now = now.Add(passkeyStepUpMaxAge + time.Minute)
	finish := browserAs(h, "ozal.localhost:9091", "/_/api/admin/passkeys/finish", ceremony, session, credential)
	if finish.Code != http.StatusForbidden || decode(t, finish)["code"] != "passkey_required" {
		t.Fatalf("finish after proof expiry = %d; want 403 passkey_required", finish.Code)
	}
	if len(ownPasskeys(t, h, session)) != 1 {
		t.Fatal("an expired proof added a credential")
	}
	// Prove ownership with the existing passkey, not the proposed new one.
	proof := decode(t, browserAs(h, "ozal.localhost:9091", "/_/api/admin/step-up/begin", "", session, map[string]any{}))
	confirmed := browserAs(h, "ozal.localhost:9091", "/_/api/admin/step-up/finish", proof["ceremony"].(string), session, first.get(t, proof["options"].(map[string]any), consoleOrigin))
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirmation = %d", confirmed.Code)
	}
	finish = browserAs(h, "ozal.localhost:9091", "/_/api/admin/passkeys/finish", ceremony, session, credential)
	if finish.Code != http.StatusOK || len(ownPasskeys(t, h, session)) != 2 {
		t.Fatalf("same registration after confirmation = %d", finish.Code)
	}
	if again := browserAs(h, "ozal.localhost:9091", "/_/api/admin/passkeys/finish", ceremony, session, credential); again.Code == http.StatusOK {
		t.Fatal("registration was replayed")
	}
}

func TestAdministratorAddRechecksFreshProofBeforeStorage(t *testing.T) {
	h, _ := identityHandler(t)
	base := time.Now()
	var offset atomic.Int64
	h.operators.now = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	session := ownerSession(t, h)
	grant, ok := h.operators.authenticate(session)
	if !ok {
		t.Fatal("session not recognized")
	}
	begin := decode(t, browserAs(h, "ozal.localhost:9091", "/_/api/admin/passkeys/begin", "", session, map[string]any{}))
	ceremony := begin["ceremony"].(string)
	credential := newAuthenticator(t).create(t, begin["options"].(map[string]any), consoleOrigin)
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, consoleOrigin+"/_/api/admin/passkeys/finish", bytes.NewReader(raw))
	req.Header.Set("Origin", consoleOrigin)
	req.Header.Set(adminCeremony, ceremony)
	req = req.WithContext(context.WithValue(req.Context(), operatorKey{}, grant))
	rec := httptest.NewRecorder()
	h.identityMu.Lock()
	done := make(chan struct{})
	go func() { defer close(done); h.addPasskeyFinish(rec, req) }()
	deadline := time.Now().Add(5 * time.Second)
	consumed := false
	for !consumed && time.Now().Before(deadline) {
		h.ceremonies.mu.Lock()
		_, pending := h.ceremonies.pending[ceremony]
		h.ceremonies.mu.Unlock()
		consumed = !pending
		if !consumed {
			time.Sleep(time.Millisecond)
		}
	}
	offset.Store(int64(passkeyStepUpMaxAge + time.Minute))
	h.identityMu.Unlock()
	<-done
	if !consumed {
		t.Fatal("finish did not pass its initial checks")
	}
	if rec.Code != http.StatusForbidden || decode(t, rec)["code"] != "passkey_required" {
		t.Fatalf("proof expiring while waiting to store = %d; want 403 passkey_required", rec.Code)
	}
	if len(ownPasskeys(t, h, session)) != 1 {
		t.Fatal("expired proof persisted a new credential")
	}
}

func TestHTTPSReauthenticationAllowsDeviceLinkAfterProofExpires(t *testing.T) {
	h, remote, _ := loginHostHandler(t, true)
	invitation, _ := hostEnrolment(t, h, "bootstrap", "")
	device := newAuthenticator(t)
	secret, challenge := verifier()
	code, _ := loginHostCeremony(t, h, remote, device, map[string]any{"challenge": challenge, "token": invitation, "name": "Owner", "email": "owner@example.com"})
	session := redeem(t, h, code, secret)["token"].(string)
	h.operators.now = func() time.Time { return time.Now().Add(passkeyStepUpMaxAge + time.Minute) }
	stale := api(h, http.MethodPost, "/_/api/admin/devices", session, nil)
	if stale.Code != http.StatusForbidden || decode(t, stale)["code"] != "passkey_required" {
		t.Fatalf("old HTTPS proof issued a link: %d", stale.Code)
	}
	stepUp := browserAs(h, "ozal.localhost:9091", "/_/api/admin/step-up/begin", "", session, map[string]any{})
	if stepUp.Code != http.StatusConflict || decode(t, stepUp)["code"] != "login_host_required" {
		t.Fatalf("HTTPS confirmation did not stay on its relying party: %d", stepUp.Code)
	}
	secret, challenge = verifier()
	code, _ = loginHostCeremony(t, h, remote, device, map[string]any{"challenge": challenge})
	fresh := redeem(t, h, code, secret)["token"].(string)
	issued := api(h, http.MethodPost, "/_/api/admin/devices", fresh, nil)
	if issued.Code != http.StatusCreated {
		t.Fatalf("link after HTTPS confirmation = %d", issued.Code)
	}
	// The one-use grant keeps its own lifetime after issuance.
	h.operators.now = func() time.Time { return time.Now().Add(2*passkeyStepUpMaxAge + 2*time.Minute) }
	joined := enrol(t, h, newAuthenticator(t), decode(t, issued)["token"].(string), "", "")
	if joined.Code != http.StatusOK {
		t.Fatalf("authorized device link failed after issuer proof expired: %d", joined.Code)
	}
}
