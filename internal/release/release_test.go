package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func keys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

const sums = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ozal_0.2.0_linux_amd64.tar.gz\n" +
	"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  ozal_0.2.0_linux_arm64.tar.gz\n"

func TestASignedChecksumsFileVerifies(t *testing.T) {
	pub, priv := keys(t)
	got, err := VerifyChecksums([]byte(sums), Sign([]byte(sums), priv), pub)
	if err != nil {
		t.Fatal(err)
	}
	if got["ozal_0.2.0_linux_amd64.tar.gz"] != strings.Repeat("a", 64) || len(got) != 2 {
		t.Fatalf("parsed %v", got)
	}
}

func TestATamperedOrForeignSignatureIsRefused(t *testing.T) {
	pub, priv := keys(t)
	otherPub, otherPriv := keys(t)
	sig := Sign([]byte(sums), priv)
	cases := map[string]struct {
		data string
		sig  string
		key  ed25519.PublicKey
	}{
		"changed hash":     {strings.Replace(sums, "aaaa", "cccc", 1), sig, pub},
		"added archive":    {sums + strings.Repeat("d", 64) + "  ozal_9.9.9_linux_amd64.tar.gz\n", sig, pub},
		"another key":      {sums, Sign([]byte(sums), otherPriv), pub},
		"wrong pinned key": {sums, sig, otherPub},
		"garbage":          {sums, "not base64!", pub},
		"empty":            {sums, "", pub},
	}
	for name, c := range cases {
		if _, err := VerifyChecksums([]byte(c.data), c.sig, c.key); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: err = %v, want ErrBadSignature", name, err)
		}
	}
}

func TestSignedButMalformedChecksumsAreRefused(t *testing.T) {
	pub, priv := keys(t)
	for name, data := range map[string]string{
		"path in name": strings.Repeat("a", 64) + "  ../ozal\n",
		"short hash":   "abc  ozal_0.2.0_linux_amd64.tar.gz\n",
		"not hex":      strings.Repeat("z", 64) + "  ozal_0.2.0_linux_amd64.tar.gz\n",
		"duplicate":    sums + strings.Repeat("e", 64) + "  ozal_0.2.0_linux_amd64.tar.gz\n",
	} {
		if _, err := VerifyChecksums([]byte(data), Sign([]byte(data), priv), pub); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestKeysRoundTripThroughText(t *testing.T) {
	pub, priv := keys(t)
	gotPub, err := ParsePublicKey(base64.StdEncoding.EncodeToString(pub) + "\n")
	if err != nil || !gotPub.Equal(pub) {
		t.Fatalf("public key: %v", err)
	}
	gotPriv, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(priv.Seed()))
	if err != nil || !gotPriv.Equal(priv) {
		t.Fatalf("private key: %v", err)
	}
	if _, err := ParsePublicKey("c2hvcnQ="); err == nil {
		t.Fatal("short public key accepted")
	}
}

func TestCompareFollowsSemverPrecedence(t *testing.T) {
	ordered := []string{"v0.1.0-alpha", "v0.1.0-alpha.1", "v0.1.0-alpha.beta", "v0.1.0-beta", "v0.1.0-beta.2", "v0.1.0-beta.11", "v0.1.0-rc.1", "v0.1.0", "0.1.1", "v0.2.0", "v1.0.0", "v1.10.0"}
	for i := range ordered {
		for j := range ordered {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := Compare(ordered[i], ordered[j]); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
	if Compare("v1.0.0+build.5", "v1.0.0") != 0 {
		t.Error("build metadata must not change precedence")
	}
	for _, bad := range []string{
		"dev", "", "v1.0", "v01.0.0", "v1.0.0-", "1.0.0-a..b", "latest",
		"1.0.0+", "1.0.0+build+extra", "1.0.0-alpha.01", "1.0.0-01",
		"1.0.0_foo", "1.0.0+bad_value", "1.0.0-élan", "1.0.0-+build",
	} {
		if valid(bad) {
			t.Errorf("%q parsed as a version", bad)
		}
	}
}

func TestDecideMovesOnlyForwardToPublishedReleases(t *testing.T) {
	releases := []Published{
		{Tag: "v0.3.0", Draft: true},
		{Tag: "v0.2.1-rc.1", Prerelease: true},
		{Tag: "v0.2.0"},
		{Tag: "v0.1.0"},
		{Tag: "nightly"},
	}
	if d := Decide("0.1.0", releases, false, false); d.Code != CodeUpdate || d.Target != "v0.2.0" {
		t.Fatalf("from 0.1.0: %+v", d)
	}
	if d := Decide("0.1.0", releases, true, false); d.Target != "v0.2.1-rc.1" {
		t.Fatalf("with prereleases: %+v", d)
	}
	if d := Decide("0.2.0", releases, false, false); d.Code != CodeCurrent {
		t.Fatalf("already current: %+v", d)
	}
	// An attacker or a mistake serving only older releases never downgrades.
	if d := Decide("0.5.0", releases, true, false); d.Code != CodeCurrent {
		t.Fatalf("downgrade offered: %+v", d)
	}
	if d := Decide("0.1.0", []Published{{Tag: "v0.3.0", Draft: true}}, true, true); d.Code != CodeCurrent {
		t.Fatalf("a draft was offered: %+v", d)
	}
}

func TestDecideNeverReplacesAnUnversionedBuildOnItsOwn(t *testing.T) {
	releases := []Published{{Tag: "v0.2.0"}}
	if d := Decide("dev", releases, false, false); d.Code != CodeUnversioned || d.Target != "" {
		t.Fatalf("dev build replaced automatically: %+v", d)
	}
	if d := Decide("dev", releases, false, true); d.Code != CodeUpdate || d.Target != "v0.2.0" {
		t.Fatalf("forced move from dev: %+v", d)
	}
	if d := Decide("dev", nil, false, true); d.Code != CodeUnversioned {
		t.Fatalf("forced move with nothing published: %+v", d)
	}
}

func TestArchiveNameMatchesGoReleaser(t *testing.T) {
	if got := ArchiveName("v0.2.0-rc.1", "linux", "amd64"); got != "ozal_0.2.0-rc.1_linux_amd64.tar.gz" {
		t.Fatal(got)
	}
}
