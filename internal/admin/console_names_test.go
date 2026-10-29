package admin

import (
	"net/http"
	"testing"
)

// REL-017: the console moves to ozal.localhost, but a
// passkey created on the legacy localhost name is bound to that RP ID. It must
// keep signing in there, so its owner can add a passkey on the new name from
// a device link instead of losing the account. Any other name is refused.
func TestLegacyLocalhostPasskeyKeepsSigningIn(t *testing.T) {
	for _, host := range []string{"localhost", "switchboard.localhost"} {
		t.Run(host, func(t *testing.T) { legacyConsolePasskey(t, host) })
	}
}

func legacyConsolePasskey(t *testing.T, hostname string) {
	t.Helper()
	h, _ := identityHandler(t)
	device := newAuthenticator(t)
	token, code := hostEnrolment(t, h, "bootstrap", "")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("bootstrap enrolment: %d", code)
	}
	legacy := hostname + ":9091"
	origin := "http://" + legacy

	begin := browser(h, legacy, "/_/api/admin/enrol/begin", "", map[string]string{"token": token, "name": "Octavio Santacruz", "email": "octavio@example.com"})
	if begin.Code != http.StatusOK {
		t.Fatalf("enrol on localhost: %d %s", begin.Code, begin.Body)
	}
	started := decode(t, begin)
	if id := started["options"].(map[string]any)["publicKey"].(map[string]any)["rp"].(map[string]any)["id"]; id != hostname {
		t.Fatalf("legacy RP ID = %v, want %s", id, hostname)
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
	legacySession := decode(t, rec)["token"].(string)
	link := api(h, http.MethodPost, "/_/api/admin/devices", legacySession, nil)
	if link.Code != http.StatusCreated {
		t.Fatalf("legacy session could not issue a migration link: %d %s", link.Code, link.Body)
	}
	newDevice := newAuthenticator(t)
	joined := enrol(t, h, newDevice, decode(t, link)["token"].(string), "", "")
	if joined.Code != http.StatusOK {
		t.Fatalf("Ozal passkey enrollment: %d %s", joined.Code, joined.Body)
	}
	if rec := signIn(t, h, newDevice); rec.Code != http.StatusOK {
		t.Fatalf("Ozal passkey login after migration: %d %s", rec.Code, rec.Body)
	}
	// A legacy assertion cannot complete a ceremony begun on Ozal's origin.
	beginOzal := browser(h, "ozal.localhost:9091", "/_/api/admin/login/begin", "", map[string]string{})
	startedOzal := decode(t, beginOzal)
	legacyLogin := decode(t, browser(h, legacy, "/_/api/admin/login/begin", "", map[string]string{}))
	assertion := device.get(t, legacyLogin["options"].(map[string]any), origin)
	if rec := browser(h, legacy, "/_/api/admin/login/finish", startedOzal["ceremony"].(string), assertion); rec.Code == http.StatusOK {
		t.Fatal("legacy origin completed an Ozal ceremony")
	}
}

func TestConsoleRefusesOtherLocalhostNames(t *testing.T) {
	h, _ := identityHandler(t)
	for _, host := range []string{"evil.localhost:9091", "ozal.localhost.example.com:9091", "switchboard.localhost.example.com:9091", "localhost.example.com:9091"} {
		if rec := browser(h, host, "/_/api/admin/login/begin", "", map[string]string{}); rec.Code == http.StatusOK {
			t.Errorf("passkey ceremony offered on %s", host)
		}
		if rec := requestWithHost(h, host, http.MethodGet, "/_/api/admin/state", "", ""); rec.Code == http.StatusOK {
			t.Errorf("console answered on %s", host)
		}
	}
	for _, host := range []string{"localhost:9091", "switchboard.localhost:9091", "ozal.localhost:9091", "OZAL.localhost:9091"} {
		if rec := requestWithHost(h, host, http.MethodGet, "/_/api/admin/state", "", ""); rec.Code != http.StatusOK {
			t.Errorf("console refused %s: %d", host, rec.Code)
		}
	}
}
