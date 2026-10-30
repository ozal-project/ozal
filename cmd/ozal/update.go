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
)

// Size limits for what an update downloads. The checksums file and signature
// are tiny; an archive is a few tens of megabytes.
const (
	maxReleaseList = 4 << 20
	maxChecksums   = 64 << 10
	maxSignature   = 1 << 10
	maxArchive     = 512 << 20
	maxBinary      = 512 << 20
)

// UpdateStatus is what the last run found, written for the console and for
// whoever reads the host. It never holds a key or a token.
type UpdateStatus struct {
	CheckedAt string `json:"checked_at"`
	Installed string `json:"installed"`
	Latest    string `json:"latest,omitempty"`
	// Result: current, unversioned, available (found, not applied),
	// updated, or failed.
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

// updateEnv is the outside world an update run touches, so tests run it
// against a fake release host and a fake upgrade.
type updateEnv struct {
	client    *http.Client
	installed func(ctx context.Context, binary string) (string, error)
	upgrade   func(ctx context.Context, binary string, args []string, out io.Writer) error
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
		upgrade: func(ctx context.Context, binary string, args []string, out io.Writer) error {
			cmd := exec.CommandContext(ctx, binary, append([]string{"upgrade"}, args...)...)
			cmd.Stdout, cmd.Stderr = out, out
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
	api := fs.String("api", "https://api.github.com/repos/ozal-project/ozal", "release host API of the repository")
	allowPre := fs.Bool("prerelease", false, "also consider prereleases (alpha, beta, rc)")
	force := fs.Bool("force", false, "replace an installed build that is not a release (once, by hand)")
	apply := fs.Bool("apply", false, "install the newer release (default: only report)")
	statusPath := fs.String("status", "", "file to write the result to, for the console (optional)")
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
		return errors.New("usage: ozal update -public-key FILE -unit UNIT -binary PATH -config PATH [-db PATH -user ACCOUNT] [-status FILE] [-prerelease] [-force] [-apply]")
	}
	if !filepath.IsAbs(*binary) {
		return errors.New("-binary must be an absolute path")
	}
	pubText, err := os.ReadFile(*pubPath)
	if err != nil {
		return fmt.Errorf("read the pinned public key: %w", err)
	}
	pub, err := release.ParsePublicKey(string(pubText))
	if err != nil {
		return err
	}

	status := UpdateStatus{CheckedAt: env.now().UTC().Format(time.RFC3339)}
	finish := func(result, reason string, err error) error {
		status.Result, status.Reason = result, reason
		fmt.Fprintf(out, "update: %s: %s\n", result, reason)
		if *statusPath != "" {
			if werr := writeUpdateStatus(*statusPath, status); werr != nil {
				fmt.Fprintf(out, "update: could not write %s: %v\n", *statusPath, werr)
				return errors.Join(err, fmt.Errorf("write update status: %w", werr))
			}
		}
		return err
	}

	installed, err := env.installed(ctx, *binary)
	if err != nil {
		return finish("failed", err.Error(), err)
	}
	status.Installed = installed
	releases, err := listReleases(ctx, env.client, *api)
	if err != nil {
		return finish("failed", err.Error(), err)
	}
	published := make([]release.Published, 0, len(releases))
	for _, r := range releases {
		published = append(published, release.Published{Tag: r.Tag, Draft: r.Draft, Prerelease: r.Prerelease})
	}
	decision := release.Decide(installed, published, *allowPre, *force)
	status.Latest = decision.Target
	switch decision.Code {
	case release.CodeCurrent:
		return finish("current", decision.Reason, nil)
	case release.CodeUnversioned:
		return finish("unversioned", decision.Reason, nil)
	}

	var chosen githubRelease
	for _, r := range releases {
		if r.Tag == decision.Target {
			chosen = r
		}
	}
	work, err := os.MkdirTemp("", "ozal-update-")
	if err != nil {
		return finish("failed", err.Error(), err)
	}
	defer os.RemoveAll(work)
	candidate, sum, err := fetchVerified(ctx, env.client, chosen, pub, work)
	if err != nil {
		return finish("failed", fmt.Sprintf("%s: %v", decision.Target, err), err)
	}
	if !*apply {
		return finish("available", fmt.Sprintf("%s (binary sha256 %s, signature verified); run with -apply to install", decision.Reason, sum[:16]), nil)
	}
	upgradeArgs := []string{"-unit", *unit, "-binary", *binary, "-sha256", sum, "-config", *configPath, "-keep", strconv.Itoa(*keep)}
	if *dbPath != "" {
		upgradeArgs = append(upgradeArgs, "-db", *dbPath, "-user", *account)
	}
	if err := env.upgrade(ctx, candidate, upgradeArgs, out); err != nil {
		return finish("failed", describeUpgradeFailure(decision.Target, installed, err), err)
	}
	return finish("updated", fmt.Sprintf("%s -> %s", installed, decision.Target), nil)
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

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
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

// fetchVerified downloads the release's signed checksums, then the archive
// for this platform, and extracts the binary only after both checks pass. It
// returns the binary's path and SHA-256.
func fetchVerified(ctx context.Context, client *http.Client, r githubRelease, pub ed25519.PublicKey, work string) (string, string, error) {
	asset := func(name string) (string, error) {
		for _, a := range r.Assets {
			if a.Name == name {
				return a.URL, nil
			}
		}
		return "", fmt.Errorf("the release has no %s", name)
	}
	sumsURL, err := asset(release.ChecksumsName)
	if err != nil {
		return "", "", err
	}
	sigURL, err := asset(release.SignatureName)
	if err != nil {
		return "", "", fmt.Errorf("%w: it was not signed, so it is not installed", err)
	}
	checksums, err := download(ctx, client, sumsURL, maxChecksums)
	if err != nil {
		return "", "", err
	}
	signature, err := download(ctx, client, sigURL, maxSignature)
	if err != nil {
		return "", "", err
	}
	sums, err := release.VerifyChecksums(checksums, string(signature), pub)
	if err != nil {
		return "", "", err
	}
	name := release.ArchiveName(r.Tag, runtime.GOOS, runtime.GOARCH)
	want, ok := sums[name]
	if !ok {
		return "", "", fmt.Errorf("the signed checksums list no %s", name)
	}
	archiveURL, err := asset(name)
	if err != nil {
		return "", "", err
	}
	archive, err := download(ctx, client, archiveURL, maxArchive)
	if err != nil {
		return "", "", err
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return "", "", fmt.Errorf("%s does not match its signed checksum", name)
	}
	path := filepath.Join(work, "ozal")
	sum, err := extractBinary(archive, path)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", name, err)
	}
	return path, sum, nil
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

// safeReleaseURL limits root-owned update downloads to GitHub release hosts.
// Loopback HTTP is permitted for deterministic local tests and private mirrors;
// public HTTP, credentials, and unrelated metadata-provided hosts are refused.
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
	return host == "github.com" || host == "api.github.com" || host == "githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
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
