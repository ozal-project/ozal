package admin

import (
	"net/http"
	"testing"
	"time"
)

func TestStepUpConfirmsTheSignedInAdministratorsOwnPasskey(t *testing.T) {
	h, _ := identityHandler(t)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	device := newAuthenticator(t)
	session := decode(t, enrol(t, h, device, token, "Octavio", "octavio@example.com"))["token"].(string)

	if rec := browserAs(h, "ozal.localhost:9091", "/_/api/admin/step-up/begin", "", "secret", map[string]any{}); rec.Code != http.StatusForbidden {
		t.Fatalf("admin token started a step-up: %d", rec.Code)
	}
	begin := browserAs(h, "ozal.localhost:9091", "/_/api/admin/step-up/begin", "", session, map[string]any{})
	if begin.Code != http.StatusOK {
		t.Fatalf("step-up begin = %d %s", begin.Code, begin.Body)
	}
	started := decode(t, begin)
	assertion := device.get(t, started["options"].(map[string]any), consoleOrigin)
	finish := browserAs(h, "ozal.localhost:9091", "/_/api/admin/step-up/finish", started["ceremony"].(string), session, assertion)
	if finish.Code != http.StatusOK || decode(t, finish)["verified"] != true {
		t.Fatalf("step-up finish = %d %s", finish.Code, finish.Body)
	}
	grant, ok := h.operators.authenticate(session)
	if !ok || !h.operators.passkeyFresh(grant.ID, passkeyStepUpMaxAge) {
		t.Fatal("the step-up did not mark the session fresh")
	}
}

func TestStepUpRefusesAnotherAdministratorsPasskeyAndDoesNotOpenASession(t *testing.T) {
	h, _ := identityHandler(t)
	first, _ := hostEnrolment(t, h, "bootstrap", "")
	ownerDevice := newAuthenticator(t)
	owner := decode(t, enrol(t, h, ownerDevice, first, "Octavio", "octavio@example.com"))["token"].(string)
	invitation, _ := invite(t, h, owner, "ana@example.com", "people")
	anaDevice := newAuthenticator(t)
	enrol(t, h, anaDevice, invitation, "Ana", "ana@example.com")

	begin := browserAs(h, "ozal.localhost:9091", "/_/api/admin/step-up/begin", "", owner, map[string]any{})
	started := decode(t, begin)
	// The discoverable prompt offers Ana's passkey; the session belongs to
	// Octavio, so the assertion must not elevate it.
	assertion := anaDevice.get(t, started["options"].(map[string]any), consoleOrigin)
	finish := browserAs(h, "ozal.localhost:9091", "/_/api/admin/step-up/finish", started["ceremony"].(string), owner, assertion)
	if finish.Code != http.StatusForbidden {
		t.Fatalf("another administrator's passkey finished the step-up: %d %s", finish.Code, finish.Body)
	}

	// A step-up ceremony cannot be replayed as a normal sign-in.
	begin = browserAs(h, "ozal.localhost:9091", "/_/api/admin/step-up/begin", "", owner, map[string]any{})
	started = decode(t, begin)
	assertion = ownerDevice.get(t, started["options"].(map[string]any), consoleOrigin)
	replayed := browser(h, "ozal.localhost:9091", "/_/api/admin/login/finish", started["ceremony"].(string), assertion)
	if replayed.Code == http.StatusOK {
		t.Fatal("a step-up ceremony opened a new login session")
	}
}

func TestPasskeyProofExpiresAndCannotComeFromTheFuture(t *testing.T) {
	s := newOperatorSessions()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	token, grant, err := s.issueSliding("Octavio", "updates.install")
	if err != nil {
		t.Fatal(err)
	}
	if !s.passkeyFresh(grant.ID, passkeyStepUpMaxAge) {
		t.Fatal("a fresh passkey session was not accepted")
	}
	now = now.Add(passkeyStepUpMaxAge + time.Second)
	if s.passkeyFresh(grant.ID, passkeyStepUpMaxAge) {
		t.Fatal("an old passkey proof was accepted")
	}
	if !s.markPasskey(grant.ID) || !s.passkeyFresh(grant.ID, passkeyStepUpMaxAge) {
		t.Fatal("a new assertion did not refresh the proof")
	}
	now = now.Add(-2 * time.Minute) // a clock that moved back
	if s.passkeyFresh(grant.ID, passkeyStepUpMaxAge) {
		t.Fatal("a future passkey proof was accepted")
	}
	host, hg, err := s.issue("Host", false, "updates.install")
	if err != nil {
		t.Fatal(err)
	}
	if hg.ID == grant.ID || s.passkeyFresh(hg.ID, passkeyStepUpMaxAge) || host == token {
		t.Fatal("a host login gained a passkey proof")
	}
}
