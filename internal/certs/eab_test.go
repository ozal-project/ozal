package certs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// verifyBinding checks the RFC 8555 section 7.3.4 envelope the way a CA
// does: the protected header names the key ID and the account URL, and the
// signature is HMAC-SHA256 of "protected.payload" under the shared key.
func verifyBinding(t *testing.T, raw []byte, keyID string, key []byte) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("new-account carried no externalAccountBinding")
	}
	var jws struct{ Protected, Payload, Signature string }
	if err := json.Unmarshal(raw, &jws); err != nil {
		t.Fatalf("binding is not a JWS: %v", err)
	}
	header, err := base64.RawURLEncoding.DecodeString(jws.Protected)
	if err != nil {
		t.Fatal(err)
	}
	var protected struct{ Alg, Kid, URL string }
	if err := json.Unmarshal(header, &protected); err != nil {
		t.Fatal(err)
	}
	if protected.Alg != "HS256" || protected.Kid != keyID || !strings.HasSuffix(protected.URL, "/new-account") {
		t.Fatalf("binding header = %+v, want HS256 with kid %q for new-account", protected, keyID)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(jws.Protected + "." + jws.Payload))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(jws.Signature)) {
		t.Fatal("binding signature does not verify under the shared key")
	}
}

func TestACMERegistrationBindsTheExternalAccount(t *testing.T) {
	const host = "app.example.com"
	key := []byte("0123456789abcdef0123456789abcdef")
	fake, server := newFakeACME(t, host)
	dns := &fakeDNS{}
	issuer, _ := testACMEIssuer(t, server, dns, &fakeResolver{dns: dns}, ACMEConfig{
		ExternalAccount: &ExternalAccount{KeyID: "kid-from-the-ca", HMACKey: key},
	})
	if _, err := issuer.clientFor(context.Background()); err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	fake.mu.Lock()
	binding := fake.binding
	fake.mu.Unlock()
	verifyBinding(t, binding, "kid-from-the-ca", key)
}

func TestACMERegistrationWithoutBindingSendsNone(t *testing.T) {
	fake, server := newFakeACME(t, "app.example.com")
	dns := &fakeDNS{}
	issuer, _ := testACMEIssuer(t, server, dns, &fakeResolver{dns: dns}, ACMEConfig{})
	if _, err := issuer.clientFor(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.binding) != 0 && string(fake.binding) != "null" {
		t.Fatalf("binding sent without being configured: %s", fake.binding)
	}
}

func TestExternalAccountMustBeComplete(t *testing.T) {
	for name, ea := range map[string]*ExternalAccount{
		"no key id": {HMACKey: []byte("secret")},
		"no key":    {KeyID: "kid"},
		"blank id":  {KeyID: "  ", HMACKey: []byte("secret")},
	} {
		t.Run(name, func(t *testing.T) {
			_, server := newFakeACME(t, "app.example.com")
			_, err := NewACMEIssuer(ACMEConfig{
				DirectoryURL: server.URL + "/directory", Email: "ops@example.com",
				DNS: &fakeDNS{}, ExternalAccount: ea,
			}, testStore(t))
			if err == nil {
				t.Fatal("an incomplete external account was accepted")
			}
		})
	}
}
