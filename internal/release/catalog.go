package release

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// CatalogSchema is the only catalog document version this client accepts.
// A newer document is refused instead of being interpreted with older rules.
const CatalogSchema = 1

// CatalogSignatureSuffix is appended to the catalog URL for its detached
// Ed25519 signature. The key is the same pinned key that signs checksums.
const CatalogSignatureSuffix = ".sig"

// Catalog is the small, signed metadata document the release service returns
// for one channel and one platform. It never contains executable
// instructions: the client chooses the fixed release binary through the
// pinned key, the signed hashes and the version rules below.
type Catalog struct {
	Schema     int    `json:"schema"`
	Channel    string `json:"channel"` // stable or preview
	Sequence   int64  `json:"sequence"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	Prerelease bool   `json:"prerelease"`
	// NotesURL is a human page, never a download or an installation input.
	NotesURL string `json:"notes_url,omitempty"`
	// Migrations says the new binary may migrate the state database, so the
	// console can warn that rollback restores the pre-upgrade database.
	Migrations bool `json:"migrations,omitempty"`
	// MinVersion is the oldest installed release that may jump directly to
	// this one. Empty means every release.
	MinVersion string `json:"min_version,omitempty"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	// ArchiveURL and its signed hash bind the binary to this version and
	// platform. ChecksumsURL and SignatureURL are the existing GoReleaser
	// pair, verified again before extraction.
	ArchiveURL    string `json:"archive_url"`
	ArchiveSHA256 string `json:"archive_sha256"`
	ArchiveSize   int64  `json:"archive_size"`
	ChecksumsURL  string `json:"checksums_url"`
	SignatureURL  string `json:"signature_url"`

	generatedAt time.Time
	expiresAt   time.Time
	publishedAt time.Time
}

// catalogWire keeps timestamps as strings so malformed documents fail with a
// clear error instead of silently producing a zero time.
type catalogWire struct {
	Schema        int    `json:"schema"`
	Channel       string `json:"channel"`
	Sequence      int64  `json:"sequence"`
	GeneratedAt   string `json:"generated_at"`
	ExpiresAt     string `json:"expires_at"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	PublishedAt   string `json:"published_at"`
	Prerelease    bool   `json:"prerelease"`
	NotesURL      string `json:"notes_url,omitempty"`
	Migrations    bool   `json:"migrations,omitempty"`
	MinVersion    string `json:"min_version,omitempty"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	ArchiveURL    string `json:"archive_url"`
	ArchiveSHA256 string `json:"archive_sha256"`
	ArchiveSize   int64  `json:"archive_size"`
	ChecksumsURL  string `json:"checksums_url"`
	SignatureURL  string `json:"signature_url"`
}

// ErrCatalogExpired means the signed metadata is outside its validity window.
var ErrCatalogExpired = errors.New("the release catalog has expired; no update is offered")

// ErrCatalogStale means a validly signed catalog is older than the newest one
// this installation already accepted. It catches a replay of signed metadata,
// not a forged one.
var ErrCatalogStale = errors.New("the release catalog is older than the last accepted one")

// ErrCatalogClock means the local clock is implausibly behind the catalog,
// so expiry cannot be judged safely.
var ErrCatalogClock = errors.New("the local clock is behind the release catalog; check the host clock")

// ParseCatalog verifies the detached signature over the exact bytes and then
// validates every field. No URL is contacted here.
func ParseCatalog(raw []byte, signature string, key ed25519.PublicKey, now time.Time) (Catalog, error) {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, raw, sig) {
		return Catalog{}, ErrBadSignature
	}
	var wire catalogWire
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Catalog{}, fmt.Errorf("release catalog: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return Catalog{}, errors.New("release catalog: more than one JSON document")
	}
	generated, err := parseCatalogTime("generated_at", wire.GeneratedAt)
	if err != nil {
		return Catalog{}, err
	}
	expires, err := parseCatalogTime("expires_at", wire.ExpiresAt)
	if err != nil {
		return Catalog{}, err
	}
	published, err := parseCatalogTime("published_at", wire.PublishedAt)
	if err != nil {
		return Catalog{}, err
	}
	c := Catalog{
		Schema: wire.Schema, Channel: wire.Channel, Sequence: wire.Sequence,
		Version: wire.Version, Commit: wire.Commit, Prerelease: wire.Prerelease,
		NotesURL: wire.NotesURL, Migrations: wire.Migrations, MinVersion: wire.MinVersion,
		OS: wire.OS, Arch: wire.Arch, ArchiveURL: wire.ArchiveURL,
		ArchiveSHA256: wire.ArchiveSHA256, ArchiveSize: wire.ArchiveSize,
		ChecksumsURL: wire.ChecksumsURL, SignatureURL: wire.SignatureURL,
		generatedAt: generated, expiresAt: expires, publishedAt: published,
	}
	if err := c.validate(); err != nil {
		return Catalog{}, err
	}
	if now.Before(generated.Add(-allowedCatalogClockSkew)) {
		return Catalog{}, ErrCatalogClock
	}
	if !now.Before(expires) {
		return Catalog{}, ErrCatalogExpired
	}
	return c, nil
}

const allowedCatalogClockSkew = 5 * time.Minute

func parseCatalogTime(field, value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("release catalog: %s must be RFC3339", field)
	}
	return t.UTC(), nil
}

func (c Catalog) validate() error {
	if c.Schema != CatalogSchema {
		return fmt.Errorf("release catalog: unsupported schema %d", c.Schema)
	}
	if c.Channel != "stable" && c.Channel != "preview" {
		return errors.New("release catalog: channel must be stable or preview")
	}
	if c.Sequence < 1 {
		return errors.New("release catalog: sequence must be positive")
	}
	if !valid(c.Version) {
		return errors.New("release catalog: version is not semantic")
	}
	pre := strings.Contains(strings.SplitN(strings.TrimPrefix(c.Version, "v"), "+", 2)[0], "-")
	if c.Prerelease != pre {
		return errors.New("release catalog: prerelease does not match the version")
	}
	if c.Channel == "stable" && c.Prerelease {
		return errors.New("release catalog: a stable channel cannot offer a prerelease")
	}
	if err := validCommit(c.Commit); err != nil {
		return err
	}
	if !c.expiresAt.After(c.generatedAt) {
		return errors.New("release catalog: expires_at must be after generated_at")
	}
	if c.publishedAt.After(c.generatedAt.Add(allowedCatalogClockSkew)) {
		return errors.New("release catalog: published_at is after generated_at")
	}
	if !platformName(c.OS) || !platformName(c.Arch) {
		return errors.New("release catalog: os and arch must be short lowercase identifiers")
	}
	if want := ArchiveName(c.Version, c.OS, c.Arch); path.Base(c.ArchiveURL) != want {
		return fmt.Errorf("release catalog: archive_url must end in %s", want)
	}
	if err := catalogURL("archive_url", c.ArchiveURL); err != nil {
		return err
	}
	if path.Base(c.ChecksumsURL) != ChecksumsName {
		return fmt.Errorf("release catalog: checksums_url must end in %s", ChecksumsName)
	}
	if err := catalogURL("checksums_url", c.ChecksumsURL); err != nil {
		return err
	}
	if path.Base(c.SignatureURL) != SignatureName {
		return fmt.Errorf("release catalog: signature_url must end in %s", SignatureName)
	}
	if err := catalogURL("signature_url", c.SignatureURL); err != nil {
		return err
	}
	if c.ArchiveSize <= 0 {
		return errors.New("release catalog: archive_size must be positive")
	}
	if !validSHA256(c.ArchiveSHA256) {
		return errors.New("release catalog: archive_sha256 must be 64 lowercase hex characters")
	}
	if c.MinVersion != "" && !valid(c.MinVersion) {
		return errors.New("release catalog: min_version is not semantic")
	}
	if c.MinVersion != "" && Compare(c.MinVersion, c.Version) > 0 {
		return errors.New("release catalog: min_version is newer than the release")
	}
	if c.NotesURL != "" {
		if err := catalogURL("notes_url", c.NotesURL); err != nil {
			return err
		}
	}
	return nil
}

func validCommit(value string) error {
	if len(value) != 40 && len(value) != 64 {
		return errors.New("release catalog: commit must be a full Git object id")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return errors.New("release catalog: commit must be hexadecimal")
	}
	if strings.ToLower(value) != value {
		return errors.New("release catalog: commit must be lowercase hexadecimal")
	}
	return nil
}

func platformName(value string) bool {
	if value == "" || len(value) > 16 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		if i > 0 && (r == '_' || r == '-') {
			continue
		}
		return false
	}
	return true
}

func catalogURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" || u.Fragment != "" {
		return fmt.Errorf("release catalog: %s must be an absolute HTTPS URL without credentials or fragment", field)
	}
	if u.Scheme != "https" && !loopbackHTTP(u) {
		return fmt.Errorf("release catalog: %s must be an absolute HTTPS URL without credentials or fragment", field)
	}
	if u.RawQuery != "" {
		return fmt.Errorf("release catalog: %s must not carry a query", field)
	}
	return nil
}

// loopbackHTTP is the same deterministic-test exception the downloader uses:
// a plain-HTTP catalog is accepted only on the local loopback interface.
func loopbackHTTP(u *url.URL) bool {
	if u == nil || u.Scheme != "http" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// GeneratedAt, ExpiresAt and PublishedAt expose the parsed validity window
// without letting a caller construct inconsistent catalog values.
func (c Catalog) GeneratedAt() time.Time { return c.generatedAt }
func (c Catalog) ExpiresAt() time.Time   { return c.expiresAt }
func (c Catalog) PublishedAt() time.Time { return c.publishedAt }

// MatchesPlatform refuses a catalog for another OS or architecture. A wrong
// platform is an explicit refusal, never a fallback to another artifact.
func (c Catalog) MatchesPlatform(goos, goarch string) error {
	if c.OS != goos || c.Arch != goarch {
		return fmt.Errorf("release catalog offers %s/%s, this host is %s/%s", c.OS, c.Arch, goos, goarch)
	}
	return nil
}

// Decision applies the same version rules as Decide, plus the catalog's
// minimum source version. It does not inspect signatures (ParseCatalog did)
// and does not install anything.
func (c Catalog) Decision(installed string, allowPrerelease, force bool) Decision {
	if c.Prerelease && c.Channel != "preview" && !allowPrerelease {
		return Decision{Code: CodeCurrent, Reason: "the newest release in this channel is a prerelease"}
	}
	d := Decide(installed, []Published{{Tag: c.Version, Prerelease: c.Prerelease}}, allowPrerelease || c.Channel == "preview", force)
	if d.Code != CodeUpdate {
		return d
	}
	if c.MinVersion != "" && valid(installed) && Compare(installed, c.MinVersion) < 0 {
		return Decision{Code: CodeIncompatible, Target: c.Version, Reason: fmt.Sprintf("%s needs at least %s installed; update through the newest release it accepts first", c.Version, c.MinVersion)}
	}
	return d
}

// CodeIncompatible means the release is new but this installation is too old
// to jump to it directly.
const CodeIncompatible = "incompatible"

// State is the newest catalog this installation verified, not necessarily the
// one it installed. It is written as soon as the signed catalog passes, so a
// later, still-valid older catalog cannot be replayed even when the operator
// only checked availability. Version is the newest catalog version seen;
// callers read the live installed version separately.
type State struct {
	Channel  string `json:"channel"`
	Sequence int64  `json:"sequence"`
	Version  string `json:"version"`
}

// ParseState reads the local rollback-protection state. A missing file is
// represented by the zero State by the caller.
func ParseState(raw []byte) (State, error) {
	var s State
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return State{}, fmt.Errorf("release catalog state: %w", err)
	}
	if s.Channel != "stable" && s.Channel != "preview" {
		return State{}, errors.New("release catalog state: channel must be stable or preview")
	}
	if s.Sequence < 0 || !valid(s.Version) {
		return State{}, errors.New("release catalog state: sequence or version is invalid")
	}
	return s, nil
}

// Accept refuses a catalog older than the last accepted one. A channel change
// is always explicit: it needs a new state, which the caller only writes after
// an operator asks for that channel.
func (s State) Accept(c Catalog) error {
	if s.Channel == "" {
		return nil
	}
	if s.Channel != c.Channel {
		return fmt.Errorf("the last accepted catalog is for the %s channel, not %s", s.Channel, c.Channel)
	}
	if c.Sequence < s.Sequence {
		return fmt.Errorf("%w: sequence %d is below %d", ErrCatalogStale, c.Sequence, s.Sequence)
	}
	if s.Version != "" && Compare(c.Version, s.Version) < 0 {
		return fmt.Errorf("%w: version %s is below the last accepted %s", ErrCatalogStale, c.Version, s.Version)
	}
	return nil
}

// ParseCatalogURL builds the detached signature URL for a catalog. Only the
// suffix is added; the request never chooses a different host.
func ParseCatalogURL(catalogURL string) (string, error) {
	u, err := url.Parse(catalogURL)
	if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "https" && !loopbackHTTP(u)) {
		return "", errors.New("catalog URL must be an absolute HTTPS URL without credentials")
	}
	return u.String() + CatalogSignatureSuffix, nil
}

// FormatSequence is used in log lines without exposing arbitrary catalog text.
func FormatSequence(sequence int64) string { return strconv.FormatInt(sequence, 10) }
