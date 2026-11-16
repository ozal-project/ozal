package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func testRelease(tag string, prerelease bool) GitHubRelease {
	base := "https://github.com/ozal-project/ozal/releases/download/" + tag + "/"
	version := strings.TrimPrefix(tag, "v")
	assets := []GitHubAsset{
		{Name: ChecksumsName, Size: 900, URL: base + ChecksumsName},
		{Name: SignatureName, Size: 89, URL: base + SignatureName},
		{Name: "ozal_" + version + "_windows_amd64.zip", Size: 10, URL: base + "ozal_" + version + "_windows_amd64.zip"},
		{Name: "ozal_" + version + "_linux_amd64.tar.gz.sbom.json", Size: 10, URL: base + "x.sbom.json"},
	}
	for _, platform := range []string{"linux_amd64", "linux_arm64", "darwin_arm64"} {
		name := "ozal_" + version + "_" + platform + ".tar.gz"
		assets = append(assets, GitHubAsset{Name: name, Size: 12345, URL: base + name})
	}
	return GitHubRelease{Tag: tag, Prerelease: prerelease, PublishedAt: "2026-11-10T12:00:00Z", HTMLURL: "https://github.com/ozal-project/ozal/releases/tag/" + tag, Assets: assets}
}

func testSums(tag string) map[string]string {
	version := strings.TrimPrefix(tag, "v")
	sums := map[string]string{}
	for _, platform := range []string{"linux_amd64", "linux_arm64", "darwin_arm64"} {
		sums["ozal_"+version+"_"+platform+".tar.gz"] = strings.Repeat("ab", 32)
	}
	return sums
}

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// Every tar.gz archive gets a catalog that an edge accepts exactly as
// `ozal update` reads it; zip archives and SBOMs are not offered.
func TestBuildCatalogsProducesWhatTheClientAccepts(t *testing.T) {
	key := testKey(t)
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	files, err := BuildCatalogs(CatalogRequest{
		Channel: "preview", Release: testRelease("v0.1.0-alpha.15", true), Commit: testCommit,
		Checksums: testSums("v0.1.0-alpha.15"), GeneratedAt: now, TTL: 14 * 24 * time.Hour, Migrations: true,
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path("preview"))
		c, err := ParseCatalog(f.Body, f.Signature, key.Public().(ed25519.PublicKey), now.Add(13*24*time.Hour))
		if err != nil {
			t.Fatalf("%s: the client refuses it: %v", f.Path("preview"), err)
		}
		if err := c.MatchesPlatform(f.OS, f.Arch); err != nil {
			t.Fatal(err)
		}
		if c.Sequence != now.Unix() || c.Version != "v0.1.0-alpha.15" || !c.Migrations || c.ArchiveSize != 12345 || c.NotesURL == "" {
			t.Fatalf("catalog = %+v", c)
		}
		if _, err := ParseCatalog(f.Body, f.Signature, key.Public().(ed25519.PublicKey), now.Add(15*24*time.Hour)); err != ErrCatalogExpired {
			t.Fatalf("after its window: %v, want expired", err)
		}
	}
	if strings.Join(paths, ",") != "preview/darwin/arm64,preview/linux/amd64,preview/linux/arm64" {
		t.Fatalf("paths = %v", paths)
	}
}

// Re-signing the same release later advances the sequence, so an edge that
// saw the newer document refuses a replay of the older one.
func TestReSignedCatalogsAdvanceTheSequence(t *testing.T) {
	key := testKey(t)
	build := func(at time.Time) Catalog {
		files, err := BuildCatalogs(CatalogRequest{Channel: "preview", Release: testRelease("v0.1.0-alpha.15", true), Commit: testCommit, Checksums: testSums("v0.1.0-alpha.15"), GeneratedAt: at, TTL: 14 * 24 * time.Hour}, key)
		if err != nil {
			t.Fatal(err)
		}
		c, err := ParseCatalog(files[0].Body, files[0].Signature, key.Public().(ed25519.PublicKey), at)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	first := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	older, newer := build(first), build(first.Add(7*24*time.Hour))
	if newer.Sequence <= older.Sequence {
		t.Fatalf("sequence %d did not advance past %d", newer.Sequence, older.Sequence)
	}
	seen := State{Channel: "preview", Sequence: newer.Sequence, Version: newer.Version}
	if err := seen.Accept(older); err == nil {
		t.Fatal("an edge accepted a replayed older catalog")
	}
}

func TestBuildCatalogsRefusesWhatShouldNotBeOffered(t *testing.T) {
	key := testKey(t)
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		mutate func(*CatalogRequest)
		want   string
	}{
		"draft":                {func(r *CatalogRequest) { r.Release.Draft = true }, "draft"},
		"prerelease on stable": {func(r *CatalogRequest) { r.Channel = "stable" }, "stable"},
		"unpublished":          {func(r *CatalogRequest) { r.Release.PublishedAt = "" }, "published"},
		"archive not in sums":  {func(r *CatalogRequest) { delete(r.Checksums, "ozal_0.1.0-alpha.15_linux_arm64.tar.gz") }, "do not list"},
		"no checksums asset":   {func(r *CatalogRequest) { r.Release.Assets = r.Release.Assets[1:] }, "must be attached"},
		"no archives":          {func(r *CatalogRequest) { r.Release.Assets = r.Release.Assets[:2] }, "no tar.gz"},
		"zero window":          {func(r *CatalogRequest) { r.TTL = 0 }, "validity"},
		"window too long":      {func(r *CatalogRequest) { r.TTL = 90 * 24 * time.Hour }, "validity"},
		"short commit":         {func(r *CatalogRequest) { r.Commit = "0123456" }, "commit"},
		"plain http archive":   {func(r *CatalogRequest) { r.Release.Assets[4].URL = "http://example.com/" + r.Release.Assets[4].Name }, "HTTPS"},
		"min version too new":  {func(r *CatalogRequest) { r.MinVersion = "v9.0.0" }, "min_version"},
		"unknown channel":      {func(r *CatalogRequest) { r.Channel = "nightly" }, "channel"},
	} {
		t.Run(name, func(t *testing.T) {
			req := CatalogRequest{Channel: "preview", Release: testRelease("v0.1.0-alpha.15", true), Commit: testCommit, Checksums: testSums("v0.1.0-alpha.15"), GeneratedAt: now, TTL: 14 * 24 * time.Hour}
			tc.mutate(&req)
			_, err := BuildCatalogs(req, key)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestChannelReleasePicksTheNewestPublishedVersion(t *testing.T) {
	releases := []GitHubRelease{
		{Tag: "v0.1.0-alpha.9", Prerelease: true},
		{Tag: "v0.1.0-alpha.10", Prerelease: true},
		{Tag: "v0.1.0-alpha.11", Prerelease: true, Draft: true},
		{Tag: "nightly", Prerelease: true},
		{Tag: "v0.1.0-alpha.8", Prerelease: false}, // flagged wrong: ignored
	}
	latest, previous, ok, err := ChannelRelease(releases, "preview")
	if err != nil || !ok || latest.Tag != "v0.1.0-alpha.10" || previous == nil || previous.Tag != "v0.1.0-alpha.9" {
		t.Fatalf("preview = %v %v %v %v", latest.Tag, previous, ok, err)
	}
	if _, _, ok, err := ChannelRelease(releases, "stable"); ok || err != nil {
		t.Fatalf("stable offered a release with only prereleases published: %v %v", ok, err)
	}
	releases = append(releases, GitHubRelease{Tag: "v0.1.0"})
	if latest, _, ok, _ := ChannelRelease(releases, "stable"); !ok || latest.Tag != "v0.1.0" {
		t.Fatalf("stable = %v", latest.Tag)
	}
	if latest, _, _, _ := ChannelRelease(releases, "preview"); latest.Tag != "v0.1.0" {
		t.Fatalf("preview must offer a newer stable release too, got %s", latest.Tag)
	}
	if _, _, _, err := ChannelRelease(releases, "beta"); err == nil {
		t.Fatal("an unknown channel was accepted")
	}
}

func TestArchivePlatformRecognizesOnlyThisRelease(t *testing.T) {
	for name, want := range map[string]bool{
		"ozal_0.1.0-alpha.15_linux_amd64.tar.gz":     true,
		"ozal_0.1.0-alpha.14_linux_amd64.tar.gz":     false,
		"ozal_0.1.0-alpha.15_linux_amd64.zip":        false,
		"ozal_0.1.0-alpha.15_linux.tar.gz":           false,
		"dir/ozal_0.1.0-alpha.15_linux_amd64.tar.gz": false,
		"ozal_0.1.0-alpha.15_Linux_amd64.tar.gz":     false,
	} {
		if _, _, ok := archivePlatform("v0.1.0-alpha.15", name); ok != want {
			t.Errorf("%s: ok = %v, want %v", name, ok, want)
		}
	}
}
