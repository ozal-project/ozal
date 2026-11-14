package passkey_test

import (
	"context"
	"encoding/hex"
	"net/http"
	"testing"
)

const (
	chromeOnMac   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36"
	safariOnPhone = "Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Mobile/15E148 Safari/604.1"
)

func passkeysOf(t *testing.T, b *browser) []map[string]any {
	t.Helper()
	rec, body := b.post(t, "/_/auth/devices/list", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("devices list = %d %s", rec.Code, rec.Body)
	}
	var out []map[string]any
	for _, p := range body["passkeys"].([]any) {
		out = append(out, p.(map[string]any))
	}
	return out
}

// A visitor tells their passkeys and sessions apart: each passkey is named
// after the browser it was made in, the one that signed this session in is
// marked, and each session names its browser.
func TestPasskeysAndSessionsNameTheirBrowser(t *testing.T) {
	h := newHarness(t)
	mac := h.browser()
	mac.agent = chromeOnMac
	laptop := newAuthenticator(t)
	if code := mac.enrol(t, laptop, h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	phone := newAuthenticator(t)
	rec, options := mac.post(t, "/_/auth/devices/add/begin", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if rec, _ := mac.post(t, "/_/auth/devices/add/finish", phone.create(t, options, loginOrigin)); rec.Code != http.StatusOK {
		t.Fatalf("add = %d", rec.Code)
	}
	iphone := h.browser()
	iphone.agent = safariOnPhone
	if code, _ := iphone.login(t, phone, "", loginOrigin); code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}

	keys := passkeysOf(t, iphone)
	if len(keys) != 2 {
		t.Fatalf("passkeys = %v", keys)
	}
	// Both were made in the same second, so the list order is by ID: find
	// each by its credential instead.
	byID := map[string]map[string]any{}
	for _, k := range keys {
		byID[k["id"].(string)] = k
	}
	first, second := byID[hex.EncodeToString(laptop.credentialID)], byID[hex.EncodeToString(phone.credentialID)]
	if first == nil || second == nil {
		t.Fatalf("passkeys = %v; want the laptop's and the phone's", keys)
	}
	if first["label"] != "Chrome on macOS" || second["label"] != "Chrome on macOS" {
		t.Fatalf("labels = %v, %v; want the browser each was made in", first["label"], second["label"])
	}
	if first["current"] != false || second["current"] != true {
		t.Fatalf("current = %v, %v; want only the passkey that signed this session in", first["current"], second["current"])
	}
	if second["last_client"] != "Safari on iPhone" || first["last_client"] != "Chrome on macOS" {
		t.Fatalf("last clients = %v, %v", first["last_client"], second["last_client"])
	}

	rec, body := iphone.post(t, "/_/auth/me/sessions", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	clients := map[string]bool{}
	for _, s := range body["sessions"].([]any) {
		s := s.(map[string]any)
		clients[s["client"].(string)] = s["current"].(bool)
	}
	if len(clients) != 2 || !clients["Safari on iPhone"] || clients["Chrome on macOS"] {
		t.Fatalf("sessions = %v; want both browsers, this one current", clients)
	}
}

func TestVisitorRenamesOnlyTheirOwnPasskey(t *testing.T) {
	h := newHarness(t)
	ana := h.browser()
	if code := ana.enrol(t, newAuthenticator(t), h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatal("enrol ana")
	}
	id := passkeysOf(t, ana)[0]["id"].(string)

	if rec, body := ana.post(t, "/_/auth/devices/rename", map[string]string{"id": id, "label": "  Bitwarden  "}); rec.Code != http.StatusOK || body["label"] != "Bitwarden" {
		t.Fatalf("rename = %d %v", rec.Code, body)
	}
	if got := passkeysOf(t, ana)[0]["label"]; got != "Bitwarden" {
		t.Fatalf("label = %v", got)
	}
	for _, bad := range []string{"", "   ", "line\nbreak", "tab\there"} {
		if rec, _ := ana.post(t, "/_/auth/devices/rename", map[string]string{"id": id, "label": bad}); rec.Code != http.StatusBadRequest {
			t.Fatalf("label %q = %d, want 400", bad, rec.Code)
		}
	}
	long := ""
	for range 61 {
		long += "a"
	}
	if rec, _ := ana.post(t, "/_/auth/devices/rename", map[string]string{"id": id, "label": long}); rec.Code != http.StatusBadRequest {
		t.Fatalf("61 characters = %d, want 400", rec.Code)
	}

	eve := h.browser()
	if code := eve.enrol(t, newAuthenticator(t), h.invite(t, "eve@example.com", false)); code != http.StatusOK {
		t.Fatal("enrol eve")
	}
	if rec, _ := eve.post(t, "/_/auth/devices/rename", map[string]string{"id": id, "label": "mine now"}); rec.Code != http.StatusNotFound {
		t.Fatalf("renaming another user's passkey = %d, want 404", rec.Code)
	}
	if got := passkeysOf(t, ana)[0]["label"]; got != "Bitwarden" {
		t.Fatalf("label after eve = %v", got)
	}

	anonymous := h.browser()
	if rec, _ := anonymous.post(t, "/_/auth/devices/rename", map[string]string{"id": id, "label": "x"}); rec.Code != http.StatusForbidden {
		t.Fatalf("without a session = %d, want 403", rec.Code)
	}
}

type adminBook struct {
	fakeAdminLogin
	emails map[string]bool
	asked  []string
}

func (a *adminBook) IsAdministrator(_ context.Context, email string) bool {
	a.asked = append(a.asked, email)
	return a.emails[email]
}

// The portal says the visitor's own email is also an administrator, and
// asks about no other address.
func TestPortalSaysTheEmailIsAlsoAnAdministrator(t *testing.T) {
	h := newHarness(t)
	book := &adminBook{emails: map[string]bool{"ana@example.com": true}}
	h.gate.WithAdminLogin(book)
	for email, want := range map[string]bool{"ana@example.com": true, "bob@example.com": false} {
		b := h.browser()
		if code := b.enrol(t, newAuthenticator(t), h.invite(t, email, false)); code != http.StatusOK {
			t.Fatalf("enrol %s", email)
		}
		rec, body := b.post(t, "/_/auth/me/data", map[string]any{})
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Code)
		}
		if got, _ := body["also_admin"].(bool); got != want {
			t.Fatalf("%s also_admin = %v, want %v", email, got, want)
		}
		if last := book.asked[len(book.asked)-1]; last != email {
			t.Fatalf("asked about %q for %s", last, email)
		}
	}
}

// An enrolment's answer says what was created, so the page can show a
// receipt and let the person name the passkey there and then. An unknown
// provider is reported as unknown, never as a place. A sign-in carries no
// receipt.
func TestEnrolmentReceiptNamesTheNewPasskey(t *testing.T) {
	h := newHarness(t)
	ana := h.browser()
	device := newAuthenticator(t)
	device.backupEligible = true
	code, result := ana.enrolTo(t, device, h.invite(t, "ana@example.com", false), "")
	if code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	created, ok := result["passkey"].(map[string]any)
	if !ok {
		t.Fatalf("no receipt in %v", result)
	}
	id, _ := created["id"].(string)
	if id == "" || id != passkeysOf(t, ana)[0]["id"] || created["provider_known"] != false || created["provider"] != "Synced passkey" {
		t.Fatalf("receipt = %v", created)
	}
	if rec, body := ana.post(t, "/_/auth/devices/rename", map[string]string{"id": id, "label": "Work laptop"}); rec.Code != http.StatusOK || body["label"] != "Work laptop" {
		t.Fatalf("naming from the receipt = %d %v", rec.Code, body)
	}
	if listed := passkeysOf(t, ana)[0]; listed["label"] != "Work laptop" || listed["provider_known"] != false {
		t.Fatalf("listed = %v", listed)
	}
	_, login := h.browser().login(t, device, "", loginOrigin)
	if _, has := login["passkey"]; has {
		t.Fatalf("a sign-in carried a receipt: %v", login)
	}
}
