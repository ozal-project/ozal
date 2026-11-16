package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/release"
)

// The publisher's output is what `ozal update -catalog` reads: written by
// `ozal release catalog`, served over HTTP, and checked by the same path an
// edge and the update executor use.
func TestReleaseCatalogIsAcceptedByTheUpdateCheck(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OZAL_TEST_RELEASE_KEY", base64.StdEncoding.EncodeToString(key.Seed()))
	tag := "v0.1.0-alpha.15"
	archive := release.ArchiveName(tag, runtime.GOOS, runtime.GOARCH)
	checksums := []byte(strings.Repeat("cd", 32) + "  " + archive + "\n")
	dir := t.TempDir()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/dl/"+release.ChecksumsName, func(w http.ResponseWriter, r *http.Request) { w.Write(checksums) })
	mux.HandleFunc("/dl/"+release.SignatureName, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, release.Sign(checksums, key)) })
	out := filepath.Join(dir, "site", "api", "v1", "releases")
	mux.Handle("/api/v1/releases/", http.StripPrefix("/api/v1/releases/", http.FileServer(http.Dir(out))))

	rel, _ := json.Marshal(release.GitHubRelease{Tag: tag, Prerelease: true, PublishedAt: "2026-11-10T12:00:00Z", HTMLURL: "https://github.com/ozal-project/ozal/releases/tag/" + tag, Assets: []release.GitHubAsset{
		{Name: release.ChecksumsName, URL: srv.URL + "/dl/" + release.ChecksumsName},
		{Name: release.SignatureName, URL: srv.URL + "/dl/" + release.SignatureName},
		{Name: archive, Size: 4096, URL: srv.URL + "/dl/" + archive},
	}})
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	args := []string{"-key", "env:OZAL_TEST_RELEASE_KEY", "-channel", "preview", "-release", write("release.json", rel),
		"-checksums", write("checksums.txt", checksums), "-signature", write("checksums.txt.sig", []byte(release.Sign(checksums, key))),
		"-commit", strings.Repeat("ab", 20), "-out", out}
	now := time.Now()
	var printed bytes.Buffer
	if err := runReleaseCatalog(args, &printed, func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(printed.String(), "preview/"+runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("output = %q", printed.String())
	}

	env := updateEnv{client: srv.Client(), now: time.Now}
	catalogURL := srv.URL + "/api/v1/releases/preview/" + runtime.GOOS + "/" + runtime.GOARCH
	decision, candidate, _, err := checkCatalog(context.Background(), env, pub, catalogURL, "preview", "v0.1.0-alpha.14", false, false, filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Code != release.CodeUpdate || candidate.Tag != tag || candidate.ArchiveSHA256 != strings.Repeat("cd", 32) {
		t.Fatalf("decision %+v candidate %+v", decision, candidate)
	}

	// Checksums signed by another key never become a catalog.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	args[11] = write("forged.sig", []byte(release.Sign(checksums, other)))
	if err := runReleaseCatalog(args, io.Discard, time.Now); err == nil {
		t.Fatal("checksums not signed by the release key were published")
	}
}

// `ozal release latest` names the release each channel offers, and nothing
// for a channel without one.
func TestReleaseLatestPrintsTheChannelRelease(t *testing.T) {
	list := filepath.Join(t.TempDir(), "releases.json")
	if err := os.WriteFile(list, []byte(`[{"tag_name":"v0.1.0-alpha.14","prerelease":true},{"tag_name":"v0.1.0-alpha.15","prerelease":true},{"tag_name":"v0.1.0-alpha.16","prerelease":true,"draft":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runReleaseLatest([]string{"-releases", list, "-channel", "preview"}, &out); err != nil || out.String() != "v0.1.0-alpha.15 v0.1.0-alpha.14\n" {
		t.Fatalf("preview = %q, %v", out.String(), err)
	}
	out.Reset()
	if err := runReleaseLatest([]string{"-releases", list, "-channel", "stable"}, &out); err != nil || out.String() != "" {
		t.Fatalf("stable = %q, %v", out.String(), err)
	}
}

// A site that answers a missing catalog with its own page means nothing is
// published for that channel; the error says so instead of blaming a key.
func TestUpdateCheckNamesAMissingCatalog(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<!doctype html><title>Ozal</title>")
	}))
	defer srv.Close()
	_, _, _, err := checkCatalog(context.Background(), updateEnv{client: srv.Client(), now: time.Now}, pub, srv.URL+"/api/v1/releases/stable/linux/amd64", "stable", "v0.1.0", false, false, filepath.Join(t.TempDir(), "state.json"))
	if err == nil || !strings.Contains(err.Error(), "no signed release catalog is published") {
		t.Fatalf("err = %v", err)
	}
}
