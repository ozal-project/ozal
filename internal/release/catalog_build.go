package release

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
)

// GitHubRelease is the part of a GitHub release (the REST API's release
// object) the catalog is built from. Assets carry the size and the download
// address; their hashes come only from the signed checksums.
type GitHubRelease struct {
	Tag         string        `json:"tag_name"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt string        `json:"published_at"`
	HTMLURL     string        `json:"html_url"`
	Assets      []GitHubAsset `json:"assets"`
}

// GitHubAsset is one file attached to a release.
type GitHubAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

// ParseGitHubRelease reads one release object.
func ParseGitHubRelease(raw []byte) (GitHubRelease, error) {
	var r GitHubRelease
	if err := json.Unmarshal(raw, &r); err != nil {
		return GitHubRelease{}, fmt.Errorf("github release: %w", err)
	}
	if r.Tag == "" {
		return GitHubRelease{}, errors.New("github release: tag_name is missing")
	}
	return r, nil
}

// ParseGitHubReleases reads the release list (GET /repos/{owner}/{repo}/releases).
func ParseGitHubReleases(raw []byte) ([]GitHubRelease, error) {
	var list []GitHubRelease
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("github releases: %w", err)
	}
	return list, nil
}

// ChannelRelease picks what a channel offers: the newest published stable
// release for "stable", the newest published release of any kind for
// "preview". Drafts and tags that are not versions are never offered. It also
// returns the release before it in the same channel, if any, so the caller
// can tell what changed between them. ok is false when the channel has
// nothing to offer yet.
func ChannelRelease(releases []GitHubRelease, channel string) (latest GitHubRelease, previous *GitHubRelease, ok bool, err error) {
	if channel != "stable" && channel != "preview" {
		return GitHubRelease{}, nil, false, errors.New("channel must be stable or preview")
	}
	eligible := make([]GitHubRelease, 0, len(releases))
	for _, r := range releases {
		if r.Draft || !valid(r.Tag) || r.Prerelease != isPrerelease(r.Tag) {
			continue
		}
		if channel == "stable" && r.Prerelease {
			continue
		}
		eligible = append(eligible, r)
	}
	if len(eligible) == 0 {
		return GitHubRelease{}, nil, false, nil
	}
	slices.SortFunc(eligible, func(a, b GitHubRelease) int { return Compare(b.Tag, a.Tag) })
	if len(eligible) > 1 {
		prev := eligible[1]
		previous = &prev
	}
	return eligible[0], previous, true, nil
}

func isPrerelease(tag string) bool {
	return strings.Contains(strings.SplitN(strings.TrimPrefix(tag, "v"), "+", 2)[0], "-")
}

// CatalogRequest is everything a channel's catalogs are built from. Hashes
// come from checksums that VerifyChecksums already proved; sizes and
// addresses from the release; the window and sequence from the caller's
// clock, so re-signing the same release later still advances the sequence.
type CatalogRequest struct {
	Channel     string
	Release     GitHubRelease
	Commit      string
	Checksums   map[string]string
	GeneratedAt time.Time
	TTL         time.Duration
	Migrations  bool
	MinVersion  string
}

// CatalogFile is one platform's signed catalog, ready to publish at
// <base>/<channel>/<os>/<arch> with its signature beside it at ".sig".
type CatalogFile struct {
	OS, Arch  string
	Body      []byte
	Signature string
}

// Path is the file's place under the catalog base.
func (f CatalogFile) Path(channel string) string {
	return channel + "/" + f.OS + "/" + f.Arch
}

// Maximum validity of a published catalog. A short window bounds how long a
// withdrawn release can still be offered; the publisher re-signs well before.
const maxCatalogTTL = 31 * 24 * time.Hour

// BuildCatalogs builds and signs one catalog per platform archive the release
// carries, and proves each with ParseCatalog under the public half of key
// exactly as an edge will, so a document the client would refuse is never
// published.
func BuildCatalogs(req CatalogRequest, key ed25519.PrivateKey) ([]CatalogFile, error) {
	r := req.Release
	if r.Draft {
		return nil, errors.New("release catalog: a draft is never offered")
	}
	if req.Channel == "stable" && r.Prerelease {
		return nil, errors.New("release catalog: the stable channel cannot offer a prerelease")
	}
	if req.TTL <= 0 || req.TTL > maxCatalogTTL {
		return nil, fmt.Errorf("release catalog: validity must be between 0 and %s", maxCatalogTTL)
	}
	published, err := time.Parse(time.RFC3339, r.PublishedAt)
	if err != nil {
		return nil, errors.New("release catalog: the release has no published_at; publish it first")
	}
	generated := req.GeneratedAt.UTC().Truncate(time.Second)
	checksumsURL, signatureURL := assetURL(r.Assets, ChecksumsName), assetURL(r.Assets, SignatureName)
	if checksumsURL == "" || signatureURL == "" {
		return nil, fmt.Errorf("release catalog: %s and %s must be attached to %s", ChecksumsName, SignatureName, r.Tag)
	}
	pub := key.Public().(ed25519.PublicKey)
	var files []CatalogFile
	for _, asset := range r.Assets {
		goos, goarch, ok := archivePlatform(r.Tag, asset.Name)
		if !ok {
			continue
		}
		sum, listed := req.Checksums[asset.Name]
		if !listed {
			return nil, fmt.Errorf("release catalog: the signed checksums do not list %s", asset.Name)
		}
		wire := catalogWire{
			Schema: CatalogSchema, Channel: req.Channel, Sequence: generated.Unix(),
			GeneratedAt: generated.Format(time.RFC3339), ExpiresAt: generated.Add(req.TTL).Format(time.RFC3339),
			Version: r.Tag, Commit: strings.ToLower(req.Commit), PublishedAt: published.UTC().Format(time.RFC3339),
			Prerelease: r.Prerelease, NotesURL: r.HTMLURL, Migrations: req.Migrations, MinVersion: req.MinVersion,
			OS: goos, Arch: goarch, ArchiveURL: asset.URL, ArchiveSHA256: sum, ArchiveSize: asset.Size,
			ChecksumsURL: checksumsURL, SignatureURL: signatureURL,
		}
		var body bytes.Buffer
		encoder := json.NewEncoder(&body)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(wire); err != nil {
			return nil, err
		}
		file := CatalogFile{OS: goos, Arch: goarch, Body: body.Bytes(), Signature: Sign(body.Bytes(), key)}
		if _, err := ParseCatalog(file.Body, file.Signature, pub, generated); err != nil {
			return nil, fmt.Errorf("release catalog for %s/%s: %w", goos, goarch, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("release catalog: %s has no tar.gz archives", r.Tag)
	}
	slices.SortFunc(files, func(a, b CatalogFile) int { return strings.Compare(a.OS+"/"+a.Arch, b.OS+"/"+b.Arch) })
	return files, nil
}

// archivePlatform recognizes ozal_<version>_<os>_<arch>.tar.gz for this
// release's version, and nothing else.
func archivePlatform(tag, name string) (goos, goarch string, ok bool) {
	prefix := "ozal_" + strings.TrimPrefix(tag, "v") + "_"
	if path.Base(name) != name || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tar.gz") {
		return "", "", false
	}
	goos, goarch, found := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".tar.gz"), "_")
	if !found || !platformName(goos) || !platformName(goarch) || ArchiveName(tag, goos, goarch) != name {
		return "", "", false
	}
	return goos, goarch, true
}

func assetURL(assets []GitHubAsset, name string) string {
	for _, a := range assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}
