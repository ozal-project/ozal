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
	"github.com/ozal-project/ozal/internal/updatejob"
	"github.com/ozal-project/ozal/internal/updatelock"
)

// fakeHost serves a release listing and its assets the way the release
// workflow publishes them: archive, checksums.txt and checksums.txt.sig.
type fakeHost struct {
	releases []githubRelease
	files    map[string][]byte
	hits     map[string]int
}

func (h *fakeHost) serve(t *testing.T) *httptest.Server {
	t.Helper()
	if h.hits == nil {
		h.hits = map[string]int{}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits[r.URL.Path]++
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
	host          *fakeHost
	srv           *httptest.Server
	key           ed25519.PrivateKey
	pubFile       string
	status        string
	lock          string
	upgrades      [][]string
	binaries      [][]byte
	failWith      error
	version       string
	beforeUpgrade func()
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
	rig := &updateRig{host: &fakeHost{files: map[string][]byte{}}, key: priv, pubFile: filepath.Join(dir, "release.pub"), status: filepath.Join(dir, "update-status.json"), lock: filepath.Join(dir, "update.lock"), version: installed}
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
		upgrade: func(_ context.Context, binary string, args []string, _ *updatelock.Lock, _ io.Writer) error {
			if r.beforeUpgrade != nil {
				r.beforeUpgrade()
			}
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
		"-lock", r.lock,
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

// publishCatalog serves a signed catalog document and its archive. The catalog
// names exactly the platform this test binary runs on, as the real service
// must for each supported platform.
func (h *fakeHost) publishCatalog(t *testing.T, base, tag string, key ed25519.PrivateKey, binary []byte, mutate func(map[string]any)) string {
	t.Helper()
	archive := tarGz(t, "ozal", binary)
	name := release.ArchiveName(tag, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	prefix := "/dl/" + tag + "/"
	h.files[prefix+name] = archive
	h.files[prefix+release.ChecksumsName] = checksums
	h.files[prefix+release.SignatureName] = []byte(release.Sign(checksums, key))
	doc := map[string]any{
		"schema":         release.CatalogSchema,
		"channel":        "stable",
		"sequence":       7,
		"generated_at":   "2026-10-30T00:00:00Z",
		"expires_at":     "2026-11-30T00:00:00Z",
		"version":        tag,
		"commit":         strings.Repeat("a", 40),
		"published_at":   "2026-10-29T12:00:00Z",
		"prerelease":     false,
		"notes_url":      "https://ozal.dev/releases/" + tag,
		"migrations":     true,
		"os":             runtime.GOOS,
		"arch":           runtime.GOARCH,
		"archive_url":    base + prefix + name,
		"archive_sha256": hex.EncodeToString(sum[:]),
		"archive_size":   len(archive),
		"checksums_url":  base + prefix + release.ChecksumsName,
		"signature_url":  base + prefix + release.SignatureName,
	}
	if mutate != nil {
		mutate(doc)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	h.files["/catalog"] = raw
	h.files["/catalog.sig"] = []byte(release.Sign(raw, key))
	return base + "/catalog"
}

func (r *updateRig) runCatalog(t *testing.T, catalogURL string, extra ...string) (string, error) {
	t.Helper()
	args := []string{"-catalog", catalogURL, "-state", filepath.Join(filepath.Dir(r.status), "catalog-state.json")}
	return r.run(t, append(args, extra...)...)
}

func (r *updateRig) archiveHits(tag string) int {
	name := release.ArchiveName(tag, runtime.GOOS, runtime.GOARCH)
	return r.host.hits["/dl/"+tag+"/"+name]
}

func TestUpdateCatalogCheckNeverDownloadsTheArchive(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new binary"), nil)
	out, err := rig.runCatalog(t, catalogURL)
	if err != nil || !strings.Contains(out, "available") {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if hits := rig.archiveHits("v0.2.0"); hits != 0 {
		t.Fatalf("a check downloaded the archive %d times", hits)
	}
	if len(rig.upgrades) != 0 {
		t.Fatal("a check started an upgrade")
	}
}

func TestUpdateCatalogInstallsTheVerifiedBinary(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new binary"), nil)
	if _, err := rig.runCatalog(t, catalogURL, "-apply"); err != nil {
		t.Fatal(err)
	}
	if len(rig.upgrades) != 1 || string(rig.binaries[0]) != "new binary" {
		t.Fatalf("installed %q", rig.binaries)
	}
	if hits := rig.archiveHits("v0.2.0"); hits != 1 {
		t.Fatalf("archive downloaded %d times, want once", hits)
	}
	state, err := os.ReadFile(filepath.Join(filepath.Dir(rig.status), "catalog-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `"sequence": 7`) || !strings.Contains(string(state), `"version": "v0.2.0"`) {
		t.Fatalf("catalog state %s", state)
	}
	if s := rig.readStatus(t); s.Result != "updated" || s.Latest != "v0.2.0" {
		t.Fatalf("status %+v", s)
	}
}

func TestUpdateCatalogRefusesExpiredMetadata(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), func(d map[string]any) {
		d["generated_at"] = "2026-10-01T00:00:00Z"
		d["published_at"] = "2026-09-30T00:00:00Z"
		d["expires_at"] = "2026-10-02T00:00:00Z"
	})
	_, err := rig.runCatalog(t, catalogURL, "-apply")
	if !errors.Is(err, release.ErrCatalogExpired) || len(rig.upgrades) != 0 {
		t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
	}
	if s := rig.readStatus(t); s.Result != "failed" {
		t.Fatalf("status %+v", s)
	}
}

func TestUpdateCatalogRefusesAReplayedOlderCatalog(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	dir := filepath.Dir(rig.status)
	if err := os.WriteFile(filepath.Join(dir, "catalog-state.json"), []byte(`{"channel":"stable","sequence":8,"version":"v0.2.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), nil)
	_, err := rig.runCatalog(t, catalogURL, "-apply")
	if !errors.Is(err, release.ErrCatalogStale) || len(rig.upgrades) != 0 {
		t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
	}
}

func TestUpdateCatalogRefusesAnotherPlatform(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), func(d map[string]any) {
		d["os"], d["arch"] = "plan9", "riscv64"
		d["archive_url"] = "https://ozal.dev/dl/ozal_0.2.0_plan9_riscv64.tar.gz"
	})
	_, err := rig.runCatalog(t, catalogURL, "-apply")
	if err == nil || !strings.Contains(err.Error(), "plan9/riscv64") || len(rig.upgrades) != 0 {
		t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
	}
}

func TestUpdateCatalogRefusesTamperedMetadataOrArchive(t *testing.T) {
	t.Run("catalog bytes changed", func(t *testing.T) {
		rig := newUpdateRig(t, "0.1.0")
		catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), nil)
		rig.host.files["/catalog"] = append(rig.host.files["/catalog"], '\n')
		_, err := rig.runCatalog(t, catalogURL, "-apply")
		if !errors.Is(err, release.ErrBadSignature) || len(rig.upgrades) != 0 {
			t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
		}
	})
	t.Run("archive changed after signing", func(t *testing.T) {
		rig := newUpdateRig(t, "0.1.0")
		catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new binary"), nil)
		name := release.ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
		rig.host.files["/dl/v0.2.0/"+name] = tarGz(t, "ozal", []byte("bad binary"))
		_, err := rig.runCatalog(t, catalogURL, "-apply")
		if err == nil || !strings.Contains(err.Error(), "signed checksum") || len(rig.upgrades) != 0 {
			t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
		}
	})
	t.Run("catalog and checksums disagree", func(t *testing.T) {
		rig := newUpdateRig(t, "0.1.0")
		catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), func(d map[string]any) {
			d["archive_sha256"] = strings.Repeat("c", 64)
		})
		_, err := rig.runCatalog(t, catalogURL, "-apply")
		if err == nil || !strings.Contains(err.Error(), "disagree") || len(rig.upgrades) != 0 {
			t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
		}
	})
}

func TestUpdateCatalogRequiresTheRequestedChannel(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), nil)
	_, err := rig.runCatalog(t, catalogURL, "-apply", "-channel", "preview")
	if err == nil || !strings.Contains(err.Error(), "stable channel, not preview") || len(rig.upgrades) != 0 {
		t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
	}
}

func TestUpdateGitHubCheckNeverDownloadsTheArchive(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	rig.host.publish(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), false)
	if _, err := rig.run(t); err != nil {
		t.Fatal(err)
	}
	if hits := rig.archiveHits("v0.2.0"); hits != 0 {
		t.Fatalf("a GitHub check downloaded the archive %d times", hits)
	}
}

func TestUpdateWritesAReviewablePlanWithoutDownloading(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), nil)
	planPath := filepath.Join(filepath.Dir(rig.status), "plan.json")
	if _, err := rig.runCatalog(t, catalogURL, "-plan", planPath); err != nil {
		t.Fatal(err)
	}
	plan, err := updatejob.ReadPlan(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Result != "available" || plan.Latest != "v0.2.0" || plan.Installed != "0.1.0" || !plan.Migrations {
		t.Fatalf("plan %+v", plan)
	}
	if plan.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("platform %q", plan.Platform)
	}
	if plan.ArchiveSHA256 == "" || plan.Size <= 0 {
		t.Fatalf("plan lacks the candidate bytes: %+v", plan)
	}
	if hits := rig.archiveHits("v0.2.0"); hits != 0 {
		t.Fatalf("a reviewed plan downloaded the archive %d times", hits)
	}
}

func TestUpdateRefusesACandidateThatChangedAfterReview(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), nil)
	_, err := rig.runCatalog(t, catalogURL, "-apply", "-expect", "v0.2.0", "-expect-sha256", strings.Repeat("c", 64))
	if err == nil || !strings.Contains(err.Error(), "changed since it was reviewed") || len(rig.upgrades) != 0 {
		t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
	}
}

func TestUpdateRecordsTheJobPhaseAndResult(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), nil)
	jobsDir := filepath.Join(filepath.Dir(rig.status), "jobs")
	store := updatejob.Store{Dir: jobsDir}
	id, err := updatejob.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(updatejob.Job{ID: id, Channel: "stable", From: "0.1.0", To: "v0.2.0", Phase: updatejob.PhaseReviewing, Result: updatejob.ResultPending}); err != nil {
		t.Fatal(err)
	}
	rig.beforeUpgrade = func() {
		j, err := store.Read(id)
		if err != nil {
			t.Errorf("read job before upgrade: %v", err)
			return
		}
		if j.Phase != updatejob.PhaseInstalling {
			t.Errorf("phase before upgrade = %s, want installing", j.Phase)
		}
	}
	if _, err := rig.runCatalog(t, catalogURL, "-apply", "-jobs", jobsDir, "-job", id); err != nil {
		t.Fatal(err)
	}
	job, err := store.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	if job.Result != updatejob.ResultUpdated || job.Phase != updatejob.PhaseComplete || job.FinishedAt == "" {
		t.Fatalf("job %+v", job)
	}
}

func TestUpdateRecordsARollbackAsRolledBack(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), nil)
	jobsDir := filepath.Join(filepath.Dir(rig.status), "jobs")
	store := updatejob.Store{Dir: jobsDir}
	id, err := updatejob.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(updatejob.Job{ID: id, Channel: "stable", From: "0.1.0", To: "v0.2.0", Phase: updatejob.PhaseReviewing, Result: updatejob.ResultPending}); err != nil {
		t.Fatal(err)
	}
	rig.failWith = exitStatusError(3)
	if _, err := rig.runCatalog(t, catalogURL, "-apply", "-jobs", jobsDir, "-job", id); err == nil {
		t.Fatal("failed upgrade reported success")
	}
	job, err := store.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	if job.Result != updatejob.ResultRolledBack || !strings.Contains(job.Reason, "restored") {
		t.Fatalf("job %+v", job)
	}
}

func TestUpdateCatalogRecordsTheObservedSequenceWithoutInstalling(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	first := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), func(d map[string]any) {
		d["sequence"] = 9
	})
	if _, err := rig.runCatalog(t, first); err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(filepath.Join(filepath.Dir(rig.status), "catalog-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `"sequence": 9`) {
		t.Fatalf("a check did not record the observed sequence: %s", state)
	}
	if len(rig.upgrades) != 0 {
		t.Fatal("a check installed")
	}
	// A still-valid, correctly signed catalog with a lower sequence is a
	// replay even though nothing was installed from the first one.
	second := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), func(d map[string]any) {
		d["sequence"] = 8
	})
	if _, err := rig.runCatalog(t, second, "-apply"); !errors.Is(err, release.ErrCatalogStale) || len(rig.upgrades) != 0 {
		t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
	}
}

func TestUpdateExpectInstalledVersionDoesNotInstallANewerCatalog(t *testing.T) {
	rig := newUpdateRig(t, "0.2.0")
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.3.0", rig.key, []byte("never reviewed"), nil)
	out, err := rig.runCatalog(t, catalogURL, "-apply", "-expect", "0.2.0", "-expect-sha256", strings.Repeat("a", 64))
	if err != nil || len(rig.upgrades) != 0 || !strings.Contains(out, "already installed") {
		t.Fatalf("err=%v upgrades=%d out=%s", err, len(rig.upgrades), out)
	}
	if s := rig.readStatus(t); s.Result != "current" {
		t.Fatalf("status %+v", s)
	}
}

func TestUpdateRefusesASecondProcessWhileTheLockIsHeld(t *testing.T) {
	rig := newUpdateRig(t, "0.1.0")
	lock, err := updatelock.Acquire(filepath.Join(filepath.Dir(rig.status), "update.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	catalogURL := rig.host.publishCatalog(t, rig.srv.URL, "v0.2.0", rig.key, []byte("new"), nil)
	_, err = rig.runCatalog(t, catalogURL, "-apply")
	if !errors.Is(err, updatelock.ErrBusy) || len(rig.upgrades) != 0 {
		t.Fatalf("err=%v upgrades=%d", err, len(rig.upgrades))
	}
}
