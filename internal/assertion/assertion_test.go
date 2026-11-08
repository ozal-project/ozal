package assertion

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	issuer   = "https://auth.example.com"
	audience = "custodia"
)

func newSigner(t *testing.T, now time.Time) (*Signer, ed25519.PublicKey) {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	return s, public
}

func TestSignAndVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s, public := newSigner(t, now)
	token, err := s.Sign(issuer, audience, Person{Subject: "passkey:abc", Email: "alice@example.com", Name: "Alice", Groups: []string{"ops", "members", "ops"}})
	if err != nil {
		t.Fatal(err)
	}
	claims, reject, err := Verify(token, []ed25519.PublicKey{public}, issuer, audience, now.Add(10*time.Second), 5*time.Second)
	if err != nil || reject != "" {
		t.Fatalf("verify = %q, %v", reject, err)
	}
	if claims.Subject != "passkey:abc" || claims.Email != "alice@example.com" || strings.Join(claims.Groups, ",") != "members,ops" || claims.ID == "" || claims.Expiry-claims.IssuedAt != 60 {
		t.Fatalf("claims = %+v", claims)
	}
	second, _ := s.Sign(issuer, audience, Person{Subject: "passkey:abc"})
	if c2, _, _ := Verify(second, []ed25519.PublicKey{public}, issuer, audience, now, 0); c2.ID == claims.ID {
		t.Fatal("two assertions share an ID")
	}
}

func TestVerifyRejects(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s, public := newSigner(t, now)
	other, otherPublic := newSigner(t, now)
	token, _ := s.Sign(issuer, audience, Person{Subject: "passkey:abc"})
	forged, _ := other.Sign(issuer, audience, Person{Subject: "passkey:abc"})

	if _, _, err := Verify(forged, []ed25519.PublicKey{public}, issuer, audience, now, 0); err == nil {
		t.Fatal("accepted a key that is not pinned")
	}
	// Same key ID trick: pin the other public key under our expectations.
	if _, _, err := Verify(token, []ed25519.PublicKey{otherPublic}, issuer, audience, now, 0); err == nil {
		t.Fatal("accepted a signature from an unpinned key")
	}
	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + strings.TrimRight(parts[1], "=") + "x." + parts[2]
	if _, _, err := Verify(tampered, []ed25519.PublicKey{public}, issuer, audience, now, 0); err == nil {
		t.Fatal("accepted a tampered payload")
	}
	if _, _, err := Verify("not.a.jws", []ed25519.PublicKey{public}, issuer, audience, now, 0); err == nil {
		t.Fatal("accepted garbage")
	}
	for _, tc := range []struct {
		name, iss, aud string
		at             time.Time
		want           string
	}{
		{"other issuer", "https://evil.example.com", audience, now, RejectIssuer},
		{"other audience", issuer, "billing", now, RejectAudience},
		{"expired", issuer, audience, now.Add(66 * time.Second), RejectExpired},
		{"within skew", issuer, audience, now.Add(61 * time.Second), ""},
		{"skew does not extend past it", issuer, audience, now.Add(70 * time.Second), RejectExpired},
		{"from the future", issuer, audience, now.Add(-30 * time.Second), RejectNotYet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, reject, err := Verify(token, []ed25519.PublicKey{public}, tc.iss, tc.aud, tc.at, 5*time.Second)
			if err != nil || reject != tc.want {
				t.Fatalf("reject = %q, %v; want %q", reject, err, tc.want)
			}
		})
	}
}

func TestCheckRejectsLongLivedAndIncompleteClaims(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	base := Claims{Issuer: issuer, Audience: audience, Subject: "s", ID: "j", IssuedAt: now.Unix(), Expiry: now.Add(Lifetime).Unix()}
	for _, tc := range []struct {
		name   string
		mutate func(*Claims)
		want   string
	}{
		{"valid", func(*Claims) {}, ""},
		{"hour-long", func(c *Claims) { c.Expiry = now.Add(time.Hour).Unix() }, RejectLifetime},
		{"no subject", func(c *Claims) { c.Subject = " " }, RejectSubject},
		{"no id", func(c *Claims) { c.ID = "" }, RejectID},
	} {
		c := base
		tc.mutate(&c)
		if got := Check(c, issuer, audience, now, 0); got != tc.want {
			t.Fatalf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	if _, err := Build(issuer, audience, Person{}, now, "j"); err == nil {
		t.Fatal("built an assertion for an anonymous visitor")
	}
}

func TestKeyFileIsCreatedOnceAndPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "assertion")
	first, err := LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateKey(dir)
	if err != nil || !first.Equal(second) {
		t.Fatalf("second load = %v; same key %v", err, first.Equal(second))
	}
	info, _ := os.Stat(filepath.Join(dir, KeyFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", info.Mode())
	}
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", d.Mode())
	}
	os.Chmod(filepath.Join(dir, KeyFile), 0o644)
	if _, err := LoadOrCreateKey(dir); err == nil {
		t.Fatal("accepted a key readable by others")
	}
	s, _ := NewSigner(first)
	pinned, err := ParsePublicKeyPEM(s.PublicKeyPEM())
	if err != nil || !pinned.Equal(first.Public()) {
		t.Fatalf("public key round trip: %v", err)
	}
}
