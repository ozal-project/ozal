package admin

import (
	"net/http"
	"testing"
)

// UI-007 (2026-10-27): the console moved to switchboard.localhost, but a
// passkey created on the legacy localhost name is bound to that RP ID. It must
// keep signing in there, so its owner can add a passkey on the new name from
// a device link instead of losing the account. Any other name is refused.
func TestLegacyLocalhostPasskeyKeepsSigningIn(t *testing.T) {
	h, _ := identityHandler(t)
	device := newAuthenticator(t)
	token, code := hostEnrolment(t, h, "bootstrap", "")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("bootstrap enrolment: %d", code)
	}
	const legacy, origin = "localhost:9091", "http://localhost:9091"

	begin := browser(h, legacy, "/_/api/admin/enrol/begin", "", map[string]string{"token": token, "name": "Octavio Santacruz", "email": "octavio@example.com"})
	if begin.Code != http.StatusOK {
		t.Fatalf("enrol on localhost: %d %s", begin.Code, begin.Body)
	}
	started := decode(t, begin)
	if id := started["options"].(map[string]any)["publicKey"].(map[string]any)["rp"].(map[string]any)["id"]; id != "localhost" {
		t.Fatalf("legacy RP ID = %v, want localhost", id)
	}
	if rec := browser(h, legacy, "/_/api/admin/enrol/finish", started["ceremony"].(string), device.create(t, started["options"].(map[string]any), origin)); rec.Code != http.StatusOK {
		t.Fatalf("enrol finish on localhost: %d %s", rec.Code, rec.Body)
	}

	login := browser(h, legacy, "/_/api/admin/login/begin", "", map[string]string{})
	if login.Code != http.StatusOK {
		t.Fatalf("login on localhost: %d %s", login.Code, login.Body)
	}
	started = decode(t, login)
	rec := browser(h, legacy, "/_/api/admin/login/finish", started["ceremony"].(string), device.get(t, started["options"].(map[string]any), origin))
	if rec.Code != http.StatusOK || decode(t, rec)["token"] == "" {
		t.Fatalf("a legacy localhost passkey no longer signs in: %d %s", rec.Code, rec.Body)
	}
}

func TestConsoleRefusesOtherLocalhostNames(t *testing.T) {
	h, _ := identityHandler(t)
	for _, host := range []string{"evil.localhost:9091", "switchboard.localhost.example.com:9091", "localhost.example.com:9091"} {
		if rec := browser(h, host, "/_/api/admin/login/begin", "", map[string]string{}); rec.Code == http.StatusOK {
			t.Errorf("passkey ceremony offered on %s", host)
		}
		if rec := requestWithHost(h, host, http.MethodGet, "/_/api/admin/state", "", ""); rec.Code == http.StatusOK {
			t.Errorf("console answered on %s", host)
		}
	}
	for _, host := range []string{"localhost:9091", "switchboard.localhost:9091", "SWITCHBOARD.localhost:9091"} {
		if rec := requestWithHost(h, host, http.MethodGet, "/_/api/admin/state", "", ""); rec.Code != http.StatusOK {
			t.Errorf("console refused %s: %d", host, rec.Code)
		}
	}
}
