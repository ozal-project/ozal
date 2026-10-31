package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/release"
	"github.com/ozal-project/ozal/internal/updatejob"
	"github.com/ozal-project/ozal/internal/updatelock"
)

// Size limits for what an update downloads. The catalog and checksums are
// tiny; an archive is a few tens of megabytes.
const (
	maxReleaseList      = 4 << 20
	maxCatalog          = 64 << 10
	maxCatalogSignature = 1 << 10
	maxCatalogState     = 16 << 10
	maxChecksums        = 64 << 10
	maxSignature        = 1 << 10
	maxArchive          = 512 << 20
	maxBinary           = 512 << 20
)

// UpdateStatus is what the last run found, written for the console and for
// whoever reads the host. It never holds a key or a token.
type UpdateStatus struct {
	CheckedAt string `json:"checked_at"`
	Installed string `json:"installed"`
	Latest    string `json:"latest,omitempty"`
	// Result: current, unversioned, available (found, not applied),
	// incompatible (needs an intermediate release), updated, or failed.
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

// updateCandidate is a fully authenticated release plan. It is built from
// signed metadata only; the archive is fetched later, and only to install.
type updateCandidate struct {
	Tag           string
	ArchiveURL    string
	ArchiveSHA256 string
	Size          int64
	Notes         string
	Migrations    bool
}

// updateEnv is the outside world an update run touches, so tests run it
// against a fake release host and a fake upgrade.
type updateEnv struct {
	client    *http.Client
	installed func(ctx context.Context, binary string) (string, error)
	upgrade   func(ctx context.Context, binary string, args []string, lock *updatelock.Lock, out io.Writer) error
	now       func() time.Time
}

func systemUpdateEnv() updateEnv {
	return updateEnv{
		client: &http.Client{Timeout: 5 * time.Minute},
		installed: func(ctx context.Context, binary string) (string, error) {
			raw, err := exec.CommandContext(ctx, binary, "version", "-json").Output()
			if err != nil {
				return "", fmt.Errorf("read the installed version: %w", err)
			}
			var info versionInfo
			if err := json.Unmarshal(raw, &info); err != nil {
				return "", fmt.Errorf("read the installed version: %w", err)
			}
			return info.Version, nil
		},
		// The new binary runs its own upgrade: it copies itself next to the
		// installed one, checks the hash, and rolls back on failure.
		upgrade: func(ctx context.Context, binary string, args []string, lock *updatelock.Lock, out io.Writer) error {
			cmd := exec.CommandContext(ctx, binary, append([]string{"upgrade"}, args...)...)
			cmd.Stdout, cmd.Stderr = out, out
			// Share the open file description, so the child keeps exclusion if
			// this parent is killed while the upgrade is still running.
			cmd.ExtraFiles = []*os.File{lock.File()}
			cmd.Env = append(os.Environ(), updatelock.EnvFD+"=3")
			return cmd.Run()
		},
		now: time.Now,
	}
}

func runUpdate(ctx context.Context, args []string, out io.Writer) error {
	return update(ctx, args, out, systemUpdateEnv())
}

func update(ctx context.Context, args []string, out io.Writer, env updateEnv) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(out)
	pubPath := fs.String("public-key", "", "file holding the pinned release public key (required)")
	api := fs.String("api", "https://api.github.com/repos/ozal-project/ozal", "release host API of the repository (used when -catalog is empty)")
	catalogURL := fs.String("catalog", "", "signed release catalog URL; for example https://ozal.dev/api/v1/releases/stable/linux/arm64")
	channel := fs.String("channel", "stable", "release channel the catalog must name: stable or preview")
	statePath := fs.String("state", "", "file recording the newest accepted catalog (default: beside -binary)")
	allowPre := fs.Bool("prerelease", false, "also consider prereleases (alpha, beta, rc)")
	force := fs.Bool("force", false, "replace an unversioned build, or knowingly accept a channel change or downgrade")
	apply := fs.Bool("apply", false, "install the newer release (default: only report)")
	statusPath := fs.String("status", "", "file to write the result to, for the console (optional)")
	planPath := fs.String("plan", "", "file to write the reviewed plan to (optional)")
	jobsDir := fs.String("jobs", "", "directory of durable update jobs (used with -job)")
	jobID := fs.String("job", "", "job id to record phases and the result")
	expectVersion := fs.String("expect", "", "the reviewed version; refuse a different candidate")
	expectSHA := fs.String("expect-sha256", "", "the reviewed archive SHA-256; refuse different bytes")
	lockPath := fs.String("lock", "", "file serializing every update (default: beside -binary)")
	unit := fs.String("unit", "", "systemd unit running the installed binary (required)")
	binary := fs.String("binary", "", "installed binary to replace, absolute (required)")
	configPath := fs.String("config", "", "routing config the unit runs with (required)")
	dbPath := fs.String("db", "", "state database the unit runs with (empty: none)")
	account := fs.String("user", "", "service account that owns the state (required with -db)")
	keep := fs.Int("keep", 3, "previous binaries to keep beside -binary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *pubPath == "" || *unit == "" || *binary == "" || *configPath == "" {
		return errors.New("usage: ozal update -public-key FILE (-catalog URL | [-api URL]) -unit UNIT -binary PATH -config PATH [-channel stable|preview] [-state FILE] [-db PATH -user ACCOUNT] [-status FILE] [-plan FILE] [-jobs DIR -job ID] [-expect TAG -expect-sha256 SHA] [-prerelease] [-force] [-apply]")
	}
	if !filepath.IsAbs(*binary) {
		return errors.New("-binary must be an absolute path")
	}
	if (*jobsDir == "") != (*jobID == "") {
		return errors.New("-jobs and -job must be used together")
	}
	if *jobsDir != "" && !filepath.IsAbs(*jobsDir) {
		return errors.New("-jobs must be an absolute path")
	}
	if (*expectVersion == "") != (*expectSHA == "") {
		return errors.New("-expect and -expect-sha256 must be used together")
	}
	if *expectSHA != "" && !validCatalogSHA(*expectSHA) {
		return errors.New("-expect-sha256 must be 64 lowercase hex characters")
	}
	if *lockPath == "" {
		*lockPath = filepath.Join(filepath.Dir(*binary), "update.lock")
	}
	if !filepath.IsAbs(*lockPath) || filepath.Clean(*lockPath) != *lockPath {
		return errors.New("-lock must be an absolute clean path")
	}
	lock, err := updatelock.Acquire(*lockPath)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	defer lock.Release()
	if *catalogURL != "" {
		if *channel != "stable" && *channel != "preview" {
			return errors.New("-channel must be stable or preview")
		}
		if *statePath == "" {
			*statePath = filepath.Join(filepath.Dir(*binary), "update-catalog-state.json")
		}
		if !filepath.IsAbs(*statePath) {
			return errors.New("-state must be an absolute path")
		}
	}
	pubText, err := os.ReadFile(*pubPath)
	if err != nil {
		return fmt.Errorf("read the pinned public key: %w", err)
	}
	pub, err := release.ParsePublicKey(string(pubText))
	if err != nil {
		return err
	}

	var jobs updatejob.Store
	if *jobsDir != "" {
		jobs = updatejob.Store{Dir: *jobsDir}
		if _, err := jobs.Read(*jobID); err != nil {
			return fmt.Errorf("read the update job: %w", err)
		}
	}
	track := func(phase string) error {
		if *jobsDir == "" {
			return nil
		}
		if _, err := jobs.Advance(*jobID, phase, "", ""); err != nil {
			return fmt.Errorf("record update phase: %w", err)
		}
		return nil
	}

	status := UpdateStatus{CheckedAt: env.now().UTC().Format(time.RFC3339)}
	finish := func(result, reason string, err error) error {
		status.Result, status.Reason = result, reason
		fmt.Fprintf(out, "update: %s: %s\n", result, reason)
		if *statusPath != "" {
			if werr := writeUpdateStatus(*statusPath, status); werr != nil {
				fmt.Fprintf(out, "update: could not write %s: %v\n", *statusPath, werr)
				err = errors.Join(err, fmt.Errorf("write update status: %w", werr))
			}
		}
		if *jobsDir != "" {
			jobResult, jobReason := updateJobOutcome(result, reason, err)
			if _, jerr := jobs.Advance(*jobID, updatejob.PhaseComplete, jobResult, jobReason); jerr != nil {
				fmt.Fprintf(out, "update: could not record the job result: %v\n", jerr)
				err = errors.Join(err, fmt.Errorf("record update job: %w", jerr))
			}
		}
		return err
	}

	installed, err := env.installed(ctx, *binary)
	if err != nil {
		return finish("failed", err.Error(), err)
	}
	status.Installed = installed

	var (
		decision  release.Decision
		candidate updateCandidate
		catalog   *release.Catalog
	)
	if *catalogURL != "" {
		decision, candidate, catalog, err = checkCatalog(ctx, env, pub, *catalogURL, *channel, installed, *allowPre, *force, *statePath)
	} else {
		decision, candidate, err = checkGitHub(ctx, env, pub, *api, installed, *allowPre, *force)
	}
	if err != nil {
		return finish("failed", err.Error(), err)
	}
	status.Latest = decision.Target
	plan := updatejob.Plan{
		CheckedAt: env.now().UTC().Format(time.RFC3339),
		Installed: installed,
		Latest:    decision.Target,
		Channel:   *channel,
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		Reason:    decision.Reason,
	}
	switch decision.Code {
	case release.CodeCurrent:
		plan.Result = "current"
	case release.CodeUnversioned:
		plan.Result = "unversioned"
	case release.CodeIncompatible:
		plan.Result = "incompatible"
	default:
		plan.Result = "available"
		plan.ArchiveSHA256 = candidate.ArchiveSHA256
		plan.Size = candidate.Size
		plan.Migrations = candidate.Migrations
		plan.NotesURL = candidate.Notes
	}
	if *planPath != "" {
		id, err := updatejob.NewID()
		if err != nil {
			return finish("failed", err.Error(), err)
		}
		plan.ID = id
		if err := updatejob.WritePlan(*planPath, plan); err != nil {
			return finish("failed", err.Error(), err)
		}
	}
	// Record the newest catalog this host verified, even when it is not
	// installed. This is separate from the live installed version and stops a
	// still-valid older catalog from being replayed after a newer one was seen.
	if catalog != nil {
		if err := writeCatalogState(*statePath, release.State{Channel: catalog.Channel, Sequence: catalog.Sequence, Version: catalog.Version}); err != nil {
			return finish("failed", err.Error(), err)
		}
	}
	if *expectVersion != "" {
		// The reviewed version is already running: changing to whatever the
		// catalog offers now would install a release the operator never saw.
		if installed == *expectVersion {
			return finish("current", fmt.Sprintf("%s is already installed; nothing to do", installed), nil)
		}
		if decision.Code != release.CodeUpdate || decision.Target != *expectVersion || candidate.ArchiveSHA256 != *expectSHA {
			reason := fmt.Sprintf("the release changed since it was reviewed: expected %s (sha256 %.16s), the signed catalog now offers %s (sha256 %.16s); review again", *expectVersion, *expectSHA, decision.Target, candidate.ArchiveSHA256)
			return finish("failed", reason, errors.New(reason))
		}
	}
	switch decision.Code {
	case release.CodeCurrent:
		return finish("current", decision.Reason, nil)
	case release.CodeUnversioned:
		return finish("unversioned", decision.Reason, nil)
	case release.CodeIncompatible:
		return finish("incompatible", decision.Reason, errors.New(decision.Reason))
	}

	if !*apply {
		detail := fmt.Sprintf("%s (archive sha256 %.16s, signature verified)", decision.Reason, candidate.ArchiveSHA256)
		if candidate.Size > 0 {
			detail = fmt.Sprintf("%s (%.1f MiB, sha256 %.16s, signature verified)", decision.Reason, float64(candidate.Size)/(1<<20), candidate.ArchiveSHA256)
		}
		if candidate.Migrations {
			detail += "; it may migrate the state database, and rollback restores the previous database"
		}
		if candidate.Notes != "" {
			detail += "; notes: " + candidate.Notes
		}
		return finish("available", detail, nil)
	}
	if err := track(updatejob.PhaseStaging); err != nil {
		return finish("failed", err.Error(), err)
	}
	work, err := os.MkdirTemp("", "ozal-update-")
	if err != nil {
		return finish("failed", err.Error(), err)
	}
	defer os.RemoveAll(work)
	binaryPath, sum, err := materialize(ctx, env.client, candidate, work)
	if err != nil {
		return finish("failed", fmt.Sprintf("%s: %v", decision.Target, err), err)
	}
	if err := track(updatejob.PhaseVerifying); err != nil {
		return finish("failed", err.Error(), err)
	}
	if err := track(updatejob.PhaseInstalling); err != nil {
		return finish("failed", err.Error(), err)
	}
	upgradeArgs := []string{"-unit", *unit, "-binary", *binary, "-sha256", sum, "-config", *configPath, "-keep", strconv.Itoa(*keep), "-lock", *lockPath}
	if *dbPath != "" {
		upgradeArgs = append(upgradeArgs, "-db", *dbPath, "-user", *account)
	}
	if err := env.upgrade(ctx, binaryPath, upgradeArgs, lock, out); err != nil {
		return finish("failed", describeUpgradeFailure(decision.Target, installed, err), err)
	}
	return finish("updated", fmt.Sprintf("%s -> %s", installed, decision.Target), nil)
}

// checkGitHub lists releases and verifies the signed checksums metadata only.
// A check never downloads the archive; that happens in materialize.
func checkGitHub(ctx context.Context, env updateEnv, pub ed25519.PublicKey, api, installed string, allowPre, force bool) (release.Decision, updateCandidate, error) {
	releases, err := listReleases(ctx, env.client, api)
	if err != nil {
		return release.Decision{}, updateCandidate{}, err
	}
	published := make([]release.Published, 0, len(releases))
	for _, r := range releases {
		published = append(published, release.Published{Tag: r.Tag, Draft: r.Draft, Prerelease: r.Prerelease})
	}
	decision := release.Decide(installed, published, allowPre, force)
	if decision.Code != release.CodeUpdate {
		return decision, updateCandidate{}, nil
	}
	var chosen githubRelease
	for _, r := range releases {
		if r.Tag == decision.Target {
			chosen = r
			break
		}
	}
	candidate, err := githubCandidate(ctx, env.client, chosen, pub)
	if err != nil {
		return decision, updateCandidate{}, err
	}
	return decision, candidate, nil
}

func githubCandidate(ctx context.Context, client *http.Client, r githubRelease, pub ed25519.PublicKey) (updateCandidate, error) {
	asset := func(name string) (githubAsset, error) {
		for _, a := range r.Assets {
			if a.Name == name {
				if a.URL == "" {
					return githubAsset{}, fmt.Errorf("the release's %s has no download URL", name)
				}
				return a, nil
			}
		}
		return githubAsset{}, fmt.Errorf("the release has no %s", name)
	}
	sumsAsset, err := asset(release.ChecksumsName)
	if err != nil {
		return updateCandidate{}, err
	}
	sigAsset, err := asset(release.SignatureName)
	if err != nil {
		return updateCandidate{}, fmt.Errorf("%w: it was not signed, so it is not installed", err)
	}
	sums, err := verifyChecksumsAssets(ctx, client, sumsAsset.URL, sigAsset.URL, pub)
	if err != nil {
		return updateCandidate{}, err
	}
	name := release.ArchiveName(r.Tag, runtime.GOOS, runtime.GOARCH)
	want, ok := sums[name]
	if !ok {
		return updateCandidate{}, fmt.Errorf("the signed checksums list no %s", name)
	}
	archive, err := asset(name)
	if err != nil {
		return updateCandidate{}, err
	}
	return updateCandidate{Tag: r.Tag, ArchiveURL: archive.URL, ArchiveSHA256: want, Size: archive.Size}, nil
}

// checkCatalog reads the small signed catalog for this platform. It verifies
// the document signature, expiry, platform, channel, rollback state and the
// signed checksums before returning a plan.
func checkCatalog(ctx context.Context, env updateEnv, pub ed25519.PublicKey, catalogURL, channel, installed string, allowPre, force bool, statePath string) (release.Decision, updateCandidate, *release.Catalog, error) {
	raw, err := download(ctx, env.client, catalogURL, maxCatalog)
	if err != nil {
		return release.Decision{}, updateCandidate{}, nil, fmt.Errorf("read the release catalog: %w", err)
	}
	sigURL, err := release.ParseCatalogURL(catalogURL)
	if err != nil {
		return release.Decision{}, updateCandidate{}, nil, err
	}
	sig, err := download(ctx, env.client, sigURL, maxCatalogSignature)
	if err != nil {
		return release.Decision{}, updateCandidate{}, nil, fmt.Errorf("read the catalog signature: %w", err)
	}
	c, err := release.ParseCatalog(raw, string(sig), pub, env.now())
	if err != nil {
		return release.Decision{}, updateCandidate{}, nil, err
	}
	if c.Channel != channel {
		return release.Decision{}, updateCandidate{}, nil, fmt.Errorf("the catalog names the %s channel, not %s", c.Channel, channel)
	}
	if err := c.MatchesPlatform(runtime.GOOS, runtime.GOARCH); err != nil {
		return release.Decision{}, updateCandidate{}, nil, err
	}
	state, err := readCatalogState(statePath)
	if err != nil {
		return release.Decision{}, updateCandidate{}, nil, err
	}
	if state.Channel != "" && state.Channel != c.Channel {
		if !force {
			return release.Decision{}, updateCandidate{}, nil, fmt.Errorf("the last accepted catalog is for the %s channel; changing channel needs -force", state.Channel)
		}
		state = release.State{}
	}
	if !force {
		if err := state.Accept(c); err != nil {
			return release.Decision{}, updateCandidate{}, nil, err
		}
	}
	decision := c.Decision(installed, allowPre, force)
	if decision.Code != release.CodeUpdate {
		return decision, updateCandidate{}, &c, nil
	}
	sums, err := verifyChecksumsAssets(ctx, env.client, c.ChecksumsURL, c.SignatureURL, pub)
	if err != nil {
		return release.Decision{}, updateCandidate{}, nil, err
	}
	want, ok := sums[release.ArchiveName(c.Version, c.OS, c.Arch)]
	if !ok {
		return release.Decision{}, updateCandidate{}, nil, fmt.Errorf("the signed checksums list no %s", release.ArchiveName(c.Version, c.OS, c.Arch))
	}
	if want != c.ArchiveSHA256 {
		return release.Decision{}, updateCandidate{}, nil, errors.New("the catalog and the signed checksums disagree about the archive hash")
	}
	return decision, updateCandidate{Tag: c.Version, ArchiveURL: c.ArchiveURL, ArchiveSHA256: want, Size: c.ArchiveSize, Notes: c.NotesURL, Migrations: c.Migrations}, &c, nil
}

// verifyChecksumsAssets downloads the tiny checksums file and its detached
// signature and proves both against the pinned key.
func verifyChecksumsAssets(ctx context.Context, client *http.Client, checksumsURL, signatureURL string, pub ed25519.PublicKey) (map[string]string, error) {
	checksums, err := download(ctx, client, checksumsURL, maxChecksums)
	if err != nil {
		return nil, err
	}
	signature, err := download(ctx, client, signatureURL, maxSignature)
	if err != nil {
		return nil, err
	}
	return release.VerifyChecksums(checksums, string(signature), pub)
}

// materialize downloads the chosen archive, checks its signed hash and size,
// and extracts the binary only after both checks pass. It returns the
// binary's path and SHA-256 for `ozal upgrade`.
func materialize(ctx context.Context, client *http.Client, candidate updateCandidate, work string) (string, string, error) {
	archive, err := download(ctx, client, candidate.ArchiveURL, maxArchive)
	if err != nil {
		return "", "", err
	}
	if candidate.Size > 0 && int64(len(archive)) != candidate.Size {
		return "", "", fmt.Errorf("the archive is %d bytes, the signed catalog says %d", len(archive), candidate.Size)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != candidate.ArchiveSHA256 {
		return "", "", fmt.Errorf("%s does not match its signed checksum", filepath.Base(candidate.ArchiveURL))
	}
	path := filepath.Join(work, "ozal")
	sum, err := extractBinary(archive, path)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", filepath.Base(candidate.ArchiveURL), err)
	}
	return path, sum, nil
}

// describeUpgradeFailure uses the stable exit codes from `ozal upgrade`.
// An arbitrary process error does not prove whether a rollback happened.
func describeUpgradeFailure(target, installed string, err error) string {
	var exited interface{ ExitCode() int }
	if errors.As(err, &exited) {
		switch exited.ExitCode() {
		case 2:
			return fmt.Sprintf("installing %s was refused before stopping the service: %v", target, err)
		case 3:
			return fmt.Sprintf("installing %s failed; the previous version %s was restored: %v", target, installed, err)
		case 4:
			return fmt.Sprintf("installing %s failed and rollback did not restore service; operator intervention is required: %v", target, err)
		}
	}
	return fmt.Sprintf("installing %s ended without a verified outcome; inspect the service and recovery state before retrying: %v", target, err)
}

// updateJobOutcome maps the update command's stable result to the journal.
// A failed run with exit 3 is a proven rollback; exit 4 is not.
func updateJobOutcome(result, reason string, err error) (string, string) {
	reason = updateJobReason(reason)
	switch result {
	case "updated", "current":
		return updatejob.ResultUpdated, reason
	case "failed":
		var exited interface{ ExitCode() int }
		if errors.As(err, &exited) {
			switch exited.ExitCode() {
			case 3:
				return updatejob.ResultRolledBack, reason
			case 4:
				return updatejob.ResultInterventionRequired, reason
			}
		}
		return updatejob.ResultFailed, reason
	default:
		return updatejob.ResultFailed, reason
	}
}

func updateJobReason(reason string) string {
	reason = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, reason)
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		reason = reason[:500]
	}
	if reason == "" {
		return "no detail was reported"
	}
	return reason
}

func validCatalogSHA(value string) bool {
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

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size,omitempty"`
}

type githubRelease struct {
	Tag        string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

func listReleases(ctx context.Context, client *http.Client, api string) ([]githubRelease, error) {
	raw, err := download(ctx, client, strings.TrimSuffix(api, "/")+"/releases?per_page=30", maxReleaseList)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	var releases []githubRelease
	if err := json.Unmarshal(raw, &releases); err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	return releases, nil
}

// extractBinary writes the archive's top-level `ozal` file, executable, and
// returns its SHA-256. The archive is already verified; this still refuses
// anything but one regular file of that name.
func extractBinary(archive []byte, dest string) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", errors.New("the archive holds no ozal binary")
		}
		if err != nil {
			return "", err
		}
		if filepath.Clean(strings.TrimPrefix(h.Name, "./")) != "ozal" {
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > maxBinary {
			return "", errors.New("the ozal entry is not a regular file of a sane size")
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		if _, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(tr, maxBinary)); err != nil {
			f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
}

func download(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !safeReleaseURL(parsed) {
		return nil, errors.New("release asset URL is not an approved HTTPS release host")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, application/octet-stream")
	req.Header.Set("User-Agent", "ozal-update")
	safeClient := *client
	previousRedirectPolicy := client.CheckRedirect
	safeClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !safeReleaseURL(next.URL) {
			return errors.New("release asset redirected to an unapproved host")
		}
		for _, prior := range via {
			if prior.URL.Scheme == "https" && next.URL.Scheme != "https" {
				return errors.New("release asset redirect downgraded HTTPS")
			}
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(next, via)
		}
		return nil
	}
	res, err := safeClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", parsed.Redacted(), res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("GET %s: larger than %d bytes", parsed.Redacted(), limit)
	}
	return body, nil
}

// safeReleaseURL limits root-owned update downloads to the project's release
// hosts. Loopback HTTP is permitted for deterministic local tests and private
// mirrors; public HTTP, credentials, and unrelated metadata-provided hosts are
// refused.
func safeReleaseURL(u *url.URL) bool {
	if u == nil || u.User != nil || u.Hostname() == "" {
		return false
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		return u.Scheme == "http" && ip.IsLoopback()
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if u.Scheme == "http" {
		return host == "localhost" || strings.HasSuffix(host, ".localhost")
	}
	if u.Scheme != "https" {
		return false
	}
	if u.Port() != "" && u.Port() != "443" {
		return false
	}
	if host == "ozal.dev" || strings.HasSuffix(host, ".ozal.dev") {
		return true
	}
	return host == "github.com" || host == "api.github.com" || host == "githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

// readCatalogState reads the root-owned rollback-protection state. A missing
// file is the zero State; a symlink or oversized file is refused.
func readCatalogState(path string) (release.State, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return release.State{}, nil
	}
	if err != nil {
		return release.State{}, fmt.Errorf("read the catalog state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxCatalogState {
		return release.State{}, errors.New("the catalog state is not a small regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return release.State{}, fmt.Errorf("read the catalog state: %w", err)
	}
	state, err := release.ParseState(raw)
	if err != nil {
		return release.State{}, err
	}
	return state, nil
}

// writeCatalogState replaces the state atomically, root-owned and private.
func writeCatalogState(path string, state release.State) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-catalog-state-")
	if err != nil {
		return fmt.Errorf("write the catalog state: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write the catalog state: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write the catalog state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write the catalog state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the catalog state: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write the catalog state: %w", err)
	}
	return nil
}

// writeUpdateStatus replaces the status file atomically, readable by the
// service account so its console can show it.
func writeUpdateStatus(path string, status UpdateStatus) error {
	raw, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-status-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
