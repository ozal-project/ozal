package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/release"
)

// fakeHost serves a release listing and its assets the way the release
// workflow publishes them: archive, checksums.txt and checksums.txt.sig.
type fakeHost struct {
	releases []githubRelease
	files    map[string][]byte
}

func (h *fakeHost) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			_ = json.NewEncoder(w).Encode(h.releases)
			return
		}
		body, ok := h.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func tarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body []byte
	}{{"LICENSE", []byte("license")}, {name, content}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// publish adds a signed release to the host and returns the binary's hash.
func (h *fakeHost) publish(t *testing.T, base, tag string, key ed25519.PrivateKey, binary []byte, prerelease bool) string {
	t.Helper()
	archive := tarGz(t, "ozal", binary)
	name := release.ArchiveName(tag, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	prefix := "/dl/" + tag + "/"
	h.files[prefix+name] = archive
	h.files[prefix+release.ChecksumsName] = checksums
	h.files[prefix+release.SignatureName] = []byte(release.Sign(checksums, key))
	h.releases = append(h.releases, githubRelease{Tag: tag, Prerelease: prerelease, Assets: []githubAsset{
		{Name: name, URL: base + prefix + name},
		{Name: release.ChecksumsName, URL: base + prefix + release.ChecksumsName},
		{Name: release.SignatureName, URL: base + prefix + release.SignatureName},
	}})
	b := sha256.Sum256(binary)
	return hex.EncodeToString(b[:])
}

type updateRig struct {
	host     *fakeHost
	srv      *httptest.Server
	key      ed25519.PrivateKey
	pubFile  string
	status   string
	upgrades [][]string
	binaries [][]byte
	failWith error
	version  string
}

type exitStatusError int

func (e exitStatusError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitStatusError) ExitCode() int { return int(e) }

func newUpdateRig(t *testing.T, installed string) *updateRig {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rig := &updateRig{host: &fakeHost{files: map[string][]byte{}}, key: priv, pubFile: filepath.Join(dir, "release.pub"), status: filepath.Join(dir, "update-status.json"), version: installed}
	if err := os.WriteFile(rig.pubFile, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rig.srv = rig.host.serve(t)
	return rig
}

func (r *updateRig) run(t *testing.T, extra ...string) (string, error) {
	t.Helper()
	env := updateEnv{
		client:    r.srv.Client(),
		installed: func(context.Context, string) (string, error) { return r.version, nil },
		upgrade: func(_ context.Context, binary string, args []string, _ io.Writer) error {
			body, err := os.ReadFile(binary)
			if err != nil {
				return err
			}
			r.binaries = append(r.binaries, body)
			r.upgrades = append(r.upgrades, args)
			return r.failWith
		},
		now: func() time.Time { return time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC) },
	}
	args := append([]string{"-public-key", r.pubFile, "-api", r.srv.URL, "-status", r.status,
		"-unit", "ozal", "-binary", "/usr/local/lib/ozal/ozal", "-config", "/etc/ozal/config.json", "-db", "/var/lib/ozal/state.db", "-user", "ozal"}, extra...)
	var out bytes.Buffer
	err := update(context.Background(), args, &out, env)
	return out.String(), err
}

func (r *updateRig) readStatus(t *testing.T) UpdateStatus {
	t.Helper()
	raw, err := os.ReadFile(r.status)
	if err != nil {
		t.Fatal(err)
	}
	var s UpdateStatus
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUpdateInstallsTheNewestSignedRelease(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	rig.host.publish(t, rig.srv.URL, "v0.1.0", rig.key, []byte("old binary"), false)
	sum := rig.host.publish(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new binary"), false)
	if _, err := rig.run(t, "-apply"); err != nil {
		t.Fatal(err)
	}
	if len(rig.upgrades) != 1 || string(rig.binaries[0]) != "new binary" {
		t.Fatalf("installed %q", rig.binaries)
	}
	args := strings.Join(rig.upgrades[0], " ")
	for _, want := range []string{"-sha256 " + sum, "-unit ozal", "-db /var/lib/ozal/state.db", "-user ozal"} {
		if !strings.Contains(args, want) {
			t.Errorf("upgrade args %q lack %q", args, want)
		}
	}
	if s := rig.readStatus(t); s.Result != "updated" || s.Latest != "v0.2.0" || s.Installed != "0.1.0" {
		t.Fatalf("status %+v", s)
	}
}

func TestUpdateWithoutApplyOnlyReports(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	rig.host.publish(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new binary"), false)
	out, err := rig.run(t)
	if err != nil || len(rig.upgrades) != 0 || !strings.Contains(out, "available") {
		t.Fatalf("err=%v upgrades=%d out=%s", err, len(rig.upgrades), out)
	}
}

// A release signed by anyone but the pinned key is never installed, even
// when it is the newest one listed.
func TestUpdateRefusesAReleaseSignedByAnotherKey(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	_, impostor, _ := ed25519.GenerateKey(rand.Reader)
	rig.host.publish(t, rig.srv.URL, "v0.2.0", impostor, []byte("evil"), false)
	if _, err := rig.run(t, "-apply"); !errors.Is(err, release.ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
	if len(rig.upgrades) != 0 {
		t.Fatal("an unsigned binary was installed")
	}
	if s := rig.readStatus(t); s.Result != "failed" {
		t.Fatalf("status %+v", s)
	}
}

func TestUpdateRefusesAnArchiveThatDoesNotMatchItsSignedHash(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	rig.host.publish(t, rig.srv.URL, "v0.2.0", rig.key, []byte("good"), false)
	name := release.ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
	rig.host.files["/dl/v0.2.0/"+name] = tarGz(t, "ozal", []byte("swapped after signing"))
	if _, err := rig.run(t, "-apply"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v", err)
	}
	if len(rig.upgrades) != 0 {
		t.Fatal("a swapped archive was installed")
	}
}

func TestUpdateRefusesAnUnsignedRelease(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	rig.host.publish(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), false)
	r := &rig.host.releases[0]
	r.Assets = slices.DeleteFunc(r.Assets, func(a githubAsset) bool { return a.Name == release.SignatureName })
	if _, err := rig.run(t, "-apply"); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("err = %v", err)
	}
	if len(rig.upgrades) != 0 {
		t.Fatal("an unsigned release was installed")
	}
}

func TestUpdateLeavesADevBuildAloneUnlessForced(t *testing.T) {
	rig := newUpdateRig(t, "dev")
	rig.host.publish(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), false)
	if _, err := rig.run(t, "-apply"); err != nil || len(rig.upgrades) != 0 {
		t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
	}
	if s := rig.readStatus(t); s.Result != "unversioned" {
		t.Fatalf("status %+v", s)
	}
	if _, err := rig.run(t, "-apply", "-force"); err != nil || len(rig.upgrades) != 1 {
		t.Fatalf("forced: err=%v upgrades=%d", err, len(rig.upgrades))
	}
}

func TestUpdateIgnoresPrereleasesUnlessAsked(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	rig.host.publish(t, rig.srv.URL, "v0.2.0-rc.1", rig.key, []byte("rc"), true)
	if _, err := rig.run(t, "-apply"); err != nil || len(rig.upgrades) != 0 {
		t.Fatalf("prerelease installed without -prerelease: %v %d", err, len(rig.upgrades))
	}
	if _, err := rig.run(t, "-apply", "-prerelease"); err != nil || len(rig.upgrades) != 1 {
		t.Fatalf("-prerelease: %v %d", err, len(rig.upgrades))
	}
}

func TestUpgradeFailureStatusMatchesTheActualExitCode(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	rig.host.publish(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), false)
	for _, tc := range []struct {
		code int
		want string
	}{
		{2, "refused before stopping the service"},
		{3, "previous version 0.1.0 was restored"},
		{4, "operator intervention is required"},
	} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			rig := newUpdateRig(t, "0.1.0")
			rig.host.publish(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), false)
			rig.failWith = exitStatusError(tc.code)
			if _, err := rig.run(t, "-apply"); err == nil {
				t.Fatal("a failed upgrade reported success")
			}
			if s := rig.readStatus(t); s.Result != "failed" || !strings.Contains(s.Reason, tc.want) {
				t.Fatalf("status %+v, want reason containing %q", s, tc.want)
			}
		})
	}
}

func TestUpdateFailsWhenStatusCannotBePersisted(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	rig.status = filepath.Join(t.TempDir(), "missing", "status.json")
	_, err := rig.run(t)
	if err == nil || !strings.Contains(err.Error(), "write update status") {
		t.Fatalf("err=%v, want status persistence failure", err)
	}
}

func TestDownloadRejectsUnapprovedHostsAndRedirects(t *testing.T) {
	if _, err := download(context.Background(), http.DefaultClient, "https://example.com/release", 1024); err == nil {
		t.Fatal("unapproved release host was contacted")
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/metadata", http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	if _, err := download(context.Background(), redirect.Client(), redirect.URL, 1024); err == nil || !strings.Contains(err.Error(), "unapproved host") {
		t.Fatalf("redirect error = %v, want unapproved-host refusal", err)
	}
}

func TestExtractBinaryTakesOnlyTheRegularOzalFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := extractBinary(tarGz(t, "ozal-helper", []byte("x")), filepath.Join(dir, "a")); err == nil {
		t.Fatal("an archive without ozal was accepted")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "ozal", Typeflag: tar.TypeSymlink, Linkname: "/etc/shadow"})
	_ = tw.Close()
	_ = gz.Close()
	if _, err := extractBinary(buf.Bytes(), filepath.Join(dir, "b")); err == nil {
		t.Fatal("a symlink named ozal was accepted")
	}
	sum, err := extractBinary(tarGz(t, "./ozal", []byte("bin")), filepath.Join(dir, "c"))
	want := sha256.Sum256([]byte("bin"))
	if err != nil || sum != hex.EncodeToString(want[:]) {
		t.Fatalf("sum=%s err=%v", sum, err)
	}
}

func TestReleaseKeygenAndSignProduceAVerifiableSignature(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "release.key")
	var out bytes.Buffer
	if err := runReleaseKeygen([]string{"-key", keyFile}, &out); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(keyFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode: %v %v", info, err)
	}
	if err := runReleaseKeygen([]string{"-key", keyFile}, io.Discard); err == nil {
		t.Fatal("keygen replaced an existing key")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	pub, err := release.ParsePublicKey(lines[len(lines)-1])
	if err != nil {
		t.Fatal(err)
	}
	sums := filepath.Join(dir, "checksums.txt")
	content := strings.Repeat("a", 64) + "  ozal_0.2.0_linux_amd64.tar.gz\n"
	if err := os.WriteFile(sums, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := filepath.Join(dir, "checksums.txt.sig")
	if err := runReleaseSign([]string{"-key", "file:" + keyFile, "-in", sums, "-out", sig}, io.Discard); err != nil {
		t.Fatal(err)
	}
	signature, _ := os.ReadFile(sig)
	if _, err := release.VerifyChecksums([]byte(content), string(signature), pub); err != nil {
		t.Fatal(err)
	}
	keyText, _ := os.ReadFile(keyFile)
	if err := runReleaseSign([]string{"-key", strings.TrimSpace(string(keyText)), "-in", sums, "-out", sig}, io.Discard); err == nil {
		t.Fatal("an inline key on the command line was accepted")
	}
	if strings.Contains(out.String(), strings.TrimSpace(string(keyText))) {
		t.Fatal("keygen printed the private key")
	}
}
