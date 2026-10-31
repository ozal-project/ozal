package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func catalogFixture(t *testing.T) (ed25519.PrivateKey, map[string]any) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, map[string]any{
		"schema":         CatalogSchema,
		"channel":        "stable",
		"sequence":       7,
		"generated_at":   "2026-10-30T00:00:00Z",
		"expires_at":     "2026-11-30T00:00:00Z",
		"version":        "v0.2.0",
		"commit":         strings.Repeat("a", 40),
		"published_at":   "2026-10-29T12:00:00Z",
		"prerelease":     false,
		"notes_url":      "https://ozal.dev/releases/v0.2.0",
		"migrations":     true,
		"os":             "linux",
		"arch":           "arm64",
		"archive_url":    "https://ozal.dev/dl/ozal_0.2.0_linux_arm64.tar.gz",
		"archive_sha256": strings.Repeat("b", 64),
		"archive_size":   1234,
		"checksums_url":  "https://ozal.dev/dl/checksums.txt",
		"signature_url":  "https://ozal.dev/dl/checksums.txt.sig",
	}
}

func signCatalog(t *testing.T, doc map[string]any, key ed25519.PrivateKey) ([]byte, string) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw, Sign(raw, key)
}

func TestParseCatalogAcceptsAndSelectsASignedRelease(t *testing.T) {
	key, doc := catalogFixture(t)
	pub := key.Public().(ed25519.PublicKey)
	raw, sig := signCatalog(t, doc, key)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	c, err := ParseCatalog(raw, sig, pub, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != "v0.2.0" || c.Channel != "stable" || c.Sequence != 7 || !c.Migrations {
		t.Fatalf("catalog %+v", c)
	}
	if err := c.MatchesPlatform("linux", "arm64"); err != nil {
		t.Fatal(err)
	}
	if d := c.Decision("0.1.0", false, false); d.Code != CodeUpdate || d.Target != "v0.2.0" {
		t.Fatalf("decision %+v", d)
	}
	if d := c.Decision("0.2.0", false, false); d.Code != CodeCurrent {
		t.Fatalf("current decision %+v", d)
	}
	if d := c.Decision("dev", false, false); d.Code != CodeUnversioned {
		t.Fatalf("unversioned decision %+v", d)
	}
}

func TestParseCatalogRefusesBadSignatureExpiryAndClock(t *testing.T) {
	key, doc := catalogFixture(t)
	pub := key.Public().(ed25519.PublicKey)
	raw, sig := signCatalog(t, doc, key)
	other, _ := catalogFixture(t)

	if _, err := ParseCatalog(raw, sig, other.Public().(ed25519.PublicKey), time.Now()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("other key: %v", err)
	}
	if _, err := ParseCatalog(append(raw, '\n'), sig, pub, time.Now()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("changed bytes: %v", err)
	}
	if _, err := ParseCatalog(raw, sig, pub, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, ErrCatalogExpired) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := ParseCatalog(raw, sig, pub, time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)); !errors.Is(err, ErrCatalogClock) {
		t.Fatalf("clock: %v", err)
	}
}

func TestParseCatalogRefusesUnknownFieldsAndTrailingDocuments(t *testing.T) {
	key, doc := catalogFixture(t)
	pub := key.Public().(ed25519.PublicKey)
	doc["command"] = "systemctl reboot"
	raw, sig := signCatalog(t, doc, key)
	if _, err := ParseCatalog(raw, sig, pub, time.Now()); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field: %v", err)
	}

	key, doc = catalogFixture(t)
	pub = key.Public().(ed25519.PublicKey)
	raw, sig = signCatalog(t, doc, key)
	raw = append(raw, []byte("\n{}")...)
	if _, err := ParseCatalog(raw, sig, pub, time.Now()); err == nil {
		t.Fatal("trailing document was accepted")
	}
}

func TestParseCatalogValidatesPlatformVersionAndArtifactContract(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"wrong archive name", func(d map[string]any) {
			d["archive_url"] = "https://ozal.dev/dl/ozal_0.2.0_linux_amd64.tar.gz"
		}, "archive_url must end"},
		{"wrong checksums name", func(d map[string]any) {
			d["checksums_url"] = "https://ozal.dev/dl/other.txt"
		}, "checksums_url must end"},
		{"http archive", func(d map[string]any) {
			d["archive_url"] = "http://ozal.dev/dl/ozal_0.2.0_linux_arm64.tar.gz"
		}, "absolute HTTPS"},
		{"credentials in URL", func(d map[string]any) {
			d["archive_url"] = "https://user:pass@ozal.dev/dl/ozal_0.2.0_linux_arm64.tar.gz"
		}, "without credentials"},
		{"short hash", func(d map[string]any) {
			d["archive_sha256"] = "abc"
		}, "archive_sha256"},
		{"uppercase hash", func(d map[string]any) {
			d["archive_sha256"] = strings.Repeat("B", 64)
		}, "lowercase"},
		{"zero size", func(d map[string]any) {
			d["archive_size"] = 0
		}, "archive_size"},
		{"short commit", func(d map[string]any) {
			d["commit"] = "abc"
		}, "commit"},
		{"non semantic version", func(d map[string]any) {
			d["version"] = "latest"
		}, "semantic"},
		{"stable prerelease", func(d map[string]any) {
			d["version"] = "v0.2.0-rc.1"
			d["prerelease"] = true
		}, "stable channel"},
		{"prerelease flag mismatch", func(d map[string]any) {
			d["prerelease"] = true
		}, "prerelease does not match"},
		{"min newer", func(d map[string]any) {
			d["min_version"] = "v0.3.0"
		}, "min_version is newer"},
		{"expiry before generated", func(d map[string]any) {
			d["expires_at"] = "2026-10-29T00:00:00Z"
		}, "expires_at"},
		{"published after generated", func(d map[string]any) {
			d["published_at"] = "2026-11-01T00:00:00Z"
		}, "published_at"},
		{"unknown schema", func(d map[string]any) {
			d["schema"] = 2
		}, "unsupported schema"},
	} {
		t.Run(test.name, func(t *testing.T) {
			key, doc := catalogFixture(t)
			test.mutate(doc)
			raw, sig := signCatalog(t, doc, key)
			if _, err := ParseCatalog(raw, sig, key.Public().(ed25519.PublicKey), time.Now()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCatalogDecisionHonoursMinimumVersionAndPreview(t *testing.T) {
	key, doc := catalogFixture(t)
	doc["min_version"] = "v0.1.5"
	raw, sig := signCatalog(t, doc, key)
	c, err := ParseCatalog(raw, sig, key.Public().(ed25519.PublicKey), time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if d := c.Decision("0.1.0", false, false); d.Code != CodeIncompatible || d.Target != "v0.2.0" {
		t.Fatalf("old client: %+v", d)
	}
	if d := c.Decision("0.2.0", false, false); d.Code != CodeCurrent {
		t.Fatalf("current: %+v", d)
	}

	key, doc = catalogFixture(t)
	doc["channel"] = "preview"
	doc["version"] = "v0.2.0-rc.1"
	doc["prerelease"] = true
	doc["archive_url"] = "https://ozal.dev/dl/ozal_0.2.0-rc.1_linux_arm64.tar.gz"
	raw, sig = signCatalog(t, doc, key)
	c, err = ParseCatalog(raw, sig, key.Public().(ed25519.PublicKey), time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if d := c.Decision("0.1.0", false, false); d.Code != CodeUpdate {
		t.Fatalf("preview channel: %+v", d)
	}
}

func TestCatalogStateStopsReplayAndChannelMixing(t *testing.T) {
	key, doc := catalogFixture(t)
	c, err := ParseCatalog(mustCatalog(t, key, doc), mustSignature(t, key, doc), key.Public().(ed25519.PublicKey), time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	state := State{Channel: "stable", Sequence: 7, Version: "v0.2.0"}
	if err := state.Accept(c); err != nil {
		t.Fatal(err)
	}
	older := c
	older.Sequence = 6
	if err := state.Accept(older); !errors.Is(err, ErrCatalogStale) {
		t.Fatalf("older sequence: %v", err)
	}
	older = c
	older.Sequence = 8
	older.Version = "v0.1.9"
	if err := state.Accept(older); !errors.Is(err, ErrCatalogStale) {
		t.Fatalf("older version: %v", err)
	}
	otherChannel := c
	otherChannel.Channel = "preview"
	if err := state.Accept(otherChannel); err == nil || !strings.Contains(err.Error(), "channel") {
		t.Fatalf("channel mix: %v", err)
	}
	if err := (State{}).Accept(c); err != nil {
		t.Fatalf("first state: %v", err)
	}
}

func mustCatalog(t *testing.T, key ed25519.PrivateKey, doc map[string]any) []byte {
	t.Helper()
	raw, _ := signCatalog(t, doc, key)
	return raw
}

func mustSignature(t *testing.T, key ed25519.PrivateKey, doc map[string]any) string {
	t.Helper()
	raw, sig := signCatalog(t, doc, key)
	_ = raw
	return sig
}

func TestParseCatalogURLAddsOnlyTheDetachedSuffix(t *testing.T) {
	got, err := ParseCatalogURL("https://ozal.dev/api/v1/releases/stable/linux/arm64")
	if err != nil || got != "https://ozal.dev/api/v1/releases/stable/linux/arm64.sig" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := ParseCatalogURL("http://ozal.dev/catalog"); err == nil {
		t.Fatal("plain HTTP catalog accepted")
	}
	if _, err := ParseCatalogURL("https://user@ozal.dev/catalog"); err == nil {
		t.Fatal("credentialed catalog accepted")
	}
}

func TestParseStateRejectsUnknownFields(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"channel": "stable", "sequence": 3, "version": "v0.2.0", "command": "reboot"})
	if _, err := ParseState(raw); err == nil {
		t.Fatal("unknown state field accepted")
	}
	valid, _ := json.Marshal(State{Channel: "stable", Sequence: 3, Version: "v0.2.0"})
	if s, err := ParseState(valid); err != nil || s.Sequence != 3 {
		t.Fatalf("state %+v err %v", s, err)
	}
}

func TestCatalogSignatureIsBase64OfTheWholeDocument(t *testing.T) {
	key, doc := catalogFixture(t)
	raw, sig := signCatalog(t, doc, key)
	decoded, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || !ed25519.Verify(key.Public().(ed25519.PublicKey), raw, decoded) {
		t.Fatalf("signature does not cover the raw document: %v", err)
	}
}
