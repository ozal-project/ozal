package pairhelper

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ozal-project/ozal/internal/ctl"
	"github.com/ozal-project/ozal/internal/release"
)

const ReleasePublicKeyPath = "/etc/ozal/release.pub"

type Runner struct {
	Profile Profile
	Stdout  io.Writer
	Stderr  io.Writer
	// exec runs a fixed external program; nil uses os/exec. Tests replace it.
	exec func(ctx context.Context, stdin io.Reader, stdout io.Writer, name string, args ...string) error
}

func (r Runner) Run(ctx context.Context, q Request) error {
	if err := r.Profile.Validate(); err != nil {
		return err
	}
	if err := q.Validate(); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("pair helper must run as root")
	}
	switch q.Action {
	case "preflight":
		return r.preflight(ctx)
	case "checkpoint":
		return r.checkpoint(ctx, q)
	case "stop":
		return r.systemctl(ctx, "stop")
	case "install":
		return r.install(ctx, q)
	case "start":
		if err := r.systemctl(ctx, "reset-failed"); err != nil {
			return err
		}
		if err := r.systemctl(ctx, "start"); err != nil {
			return err
		}
		return r.waitHealth(ctx)
	case "ready":
		return r.ready(ctx, q)
	case "restore":
		return r.restore(ctx, q)
	case "report":
		return r.report(ctx, q.Progress)
	default:
		return errors.New("unknown pair helper action")
	}
}

func (r Runner) run(ctx context.Context, stdin io.Reader, stdout io.Writer, name string, args ...string) error {
	if r.exec != nil {
		return r.exec(ctx, stdin, stdout, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, r.Stderr
	return cmd.Run()
}

func (r Runner) command(ctx context.Context, name string, args ...string) error {
	if err := r.run(ctx, nil, r.Stdout, name, args...); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

func (r Runner) systemctl(ctx context.Context, action string) error {
	switch action {
	case "stop", "start", "reset-failed", "is-active":
	default:
		return errors.New("invalid systemd action")
	}
	return r.command(ctx, "/usr/bin/systemctl", action, r.Profile.Unit)
}

func (r Runner) preflight(ctx context.Context) error {
	p := r.Profile
	if err := r.command(ctx, "/usr/bin/systemctl", "is-active", "--quiet", p.Unit); err != nil {
		return fmt.Errorf("edge service is not active: %w", err)
	}
	for _, file := range []string{p.Binary, p.Config, p.StateDB} {
		st, err := os.Lstat(file)
		if err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("required edge file is unavailable or not a regular file: %s", file)
		}
	}
	if _, err := os.Stat(filepath.Join(p.Stage, "ozal.next")); err == nil {
		return errors.New("a staged candidate already exists; inspect it before proceeding")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect staging path: %w", err)
	}
	return r.health(ctx)
}

func (r Runner) checkpoint(ctx context.Context, q Request) error {
	p := r.Profile
	backup := p.Binary + ".pair-" + q.Job + ".bak"
	if _, err := os.Lstat(backup); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("pair binary checkpoint already exists or cannot be inspected")
	}
	if err := r.command(ctx, "/usr/bin/cp", "-p", p.Binary, backup); err != nil {
		return err
	}
	got, err := fileHash(backup)
	if err != nil || got != q.SHA256 {
		_ = os.Remove(backup)
		return errors.New("binary checkpoint hash differs from the installed binary")
	}
	dbBackup := p.StateDB + ".pair-" + q.Job + ".bak.db"
	if _, err := os.Lstat(dbBackup); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("pair database checkpoint already exists or cannot be inspected")
	}
	if err := r.command(ctx, p.Binary, "db", "backup", "-db", p.StateDB, "-out", dbBackup); err != nil {
		_ = os.Remove(backup)
		_ = os.Remove(dbBackup)
		return err
	}
	return nil
}

func (r Runner) install(ctx context.Context, q Request) error {
	p := r.Profile
	keyInfo, err := os.Lstat(ReleasePublicKeyPath)
	if err != nil || !keyInfo.Mode().IsRegular() || keyInfo.Mode().Perm()&0o022 != 0 {
		return errors.New("pinned release key must be a regular root-owned file, not writable by group or others")
	}
	keyStat, ok := keyInfo.Sys().(*syscall.Stat_t)
	if !ok || keyStat.Uid != 0 {
		return errors.New("pinned release key must be owned by root")
	}
	keyBytes, err := os.ReadFile(ReleasePublicKeyPath)
	if err != nil {
		return fmt.Errorf("read pinned release public key: %w", err)
	}
	key, err := release.ParsePublicKey(string(keyBytes))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("invalid pinned release public key")
	}
	data, _, err := release.FetchBinary(ctx, &http.Client{Timeout: 5 * time.Minute}, "https://api.github.com/repos/ozal-project/ozal", q.Ref, runtime.GOOS, runtime.GOARCH, key)
	if err != nil {
		return fmt.Errorf("verify signed release %s: %w", q.Ref, err)
	}
	candidateHash := digest(data)
	if candidateHash != q.SHA256 {
		return errors.New("signed release binary differs from the coordinated candidate")
	}
	versionJSON, err := exec.CommandContext(ctx, p.Binary, "version", "-json").Output()
	if err != nil {
		return fmt.Errorf("read installed edge version: %w", err)
	}
	var installed struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(versionJSON, &installed); err != nil {
		return errors.New("installed edge returned malformed version data")
	}
	installedHash, err := fileHash(p.Binary)
	if err != nil {
		return fmt.Errorf("hash installed edge binary: %w", err)
	}
	if err := ValidateUpgrade(installed.Version, installedHash, q.Ref, candidateHash); err != nil {
		return err
	}
	if err := os.MkdirAll("/root", 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("/root", "ozal-pair-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	next := filepath.Join(tmp, "ozal")
	if err := writeExecutable(data, next); err != nil {
		return err
	}
	args := []string{"upgrade", "-unit", p.Unit, "-binary", p.Binary, "-sha256", q.SHA256,
		"-config", p.Config, "-db", p.StateDB, "-user", p.ServiceUser, "-keep", fmt.Sprint(p.Keep), "-defer-start"}
	if err := r.command(ctx, next, args...); err != nil {
		return err
	}
	// Keep the forced-command entry point in sync with the signed candidate;
	// otherwise a later release could change the request protocol while this
	// separately installed helper remained on an older version.
	helperTmp := "/usr/local/sbin/.ozal-pair-helper.next"
	_ = os.Remove(helperTmp)
	if err := copyExecutable(next, helperTmp); err != nil {
		return fmt.Errorf("update pair helper after edge cutover: %w", err)
	}
	if err := os.Chmod(helperTmp, 0o755); err != nil {
		_ = os.Remove(helperTmp)
		return err
	}
	if err := os.Rename(helperTmp, "/usr/local/sbin/ozal-pair-helper"); err != nil {
		_ = os.Remove(helperTmp)
		return fmt.Errorf("replace pair helper after edge cutover: %w", err)
	}
	return nil
}

func (r Runner) ready(ctx context.Context, q Request) error {
	got, err := fileHash(r.Profile.Binary)
	if err != nil || got != q.SHA256 {
		return errors.New("installed edge binary hash does not match the candidate")
	}
	if err := r.systemctl(ctx, "is-active"); err != nil {
		return err
	}
	return r.waitReady(ctx)
}

func (r Runner) restore(ctx context.Context, q Request) error {
	p := r.Profile
	backup := p.Binary + ".pair-" + q.Job + ".bak"
	dbBackup := p.StateDB + ".pair-" + q.Job + ".bak.db"
	if st, err := os.Lstat(backup); err != nil || !st.Mode().IsRegular() {
		return errors.New("pair binary checkpoint is unavailable or not a regular file")
	}
	if st, err := os.Lstat(dbBackup); err != nil || !st.Mode().IsRegular() {
		return errors.New("pair database checkpoint is unavailable or not a regular file")
	}
	if err := r.command(ctx, "/usr/bin/cp", "-p", backup, p.Binary+".restore-"+q.Job); err != nil {
		return err
	}
	if err := os.Rename(p.Binary+".restore-"+q.Job, p.Binary); err != nil {
		return err
	}
	restored := p.StateDB + ".restore-" + q.Job
	if err := r.command(ctx, p.Binary, "db", "restore", "-from", dbBackup, "-to", restored); err != nil {
		return err
	}
	if err := r.command(ctx, "/usr/bin/chown", p.ServiceUser+":"+p.ServiceGroup, restored); err != nil {
		return err
	}
	_ = os.Remove(p.StateDB + "-wal")
	_ = os.Remove(p.StateDB + "-shm")
	failed := p.StateDB + ".failed-" + q.Job
	if err := os.Rename(p.StateDB, failed); err != nil {
		return err
	}
	return os.Rename(restored, p.StateDB)
}

func (r Runner) report(ctx context.Context, payload []byte) error {
	var progress ctl.PairProgress
	if err := json.Unmarshal(payload, &progress); err != nil {
		return errors.New("invalid pair progress")
	}
	args := []string{"--silent", "--show-error", "--max-time", "5", "--unix-socket", r.Profile.Socket,
		"-H", "Content-Type: application/json", "--data-binary", "@-", "-o", "/dev/null", "-w", "%{http_code}", "http://localhost/_/api/updates/status"}
	var out strings.Builder
	if err := r.run(ctx, strings.NewReader(string(payload)), &out, "/usr/bin/curl", args...); err != nil {
		return fmt.Errorf("update status API did not answer: %w", err)
	}
	return statusAccepted(strings.TrimSpace(out.String()), progress)
}

// statusAccepted requires 202 once the candidate edge runs. Before that, an
// older edge may not know the endpoint and answer 404 or 405.
func statusAccepted(code string, progress ctl.PairProgress) error {
	switch code {
	case "202":
		return nil
	case "404", "405":
		if progress.Result == "current" || progress.Phase == "staging" || progress.Phase == "checkpointing" || progress.Phase == "stopping" {
			return nil
		}
		return fmt.Errorf("update status API returned %s after the candidate edge started", code)
	default:
		return fmt.Errorf("update status API returned %s", code)
	}
}

func (r Runner) health(ctx context.Context) error {
	if err := r.run(ctx, nil, io.Discard, "/usr/bin/curl", "--silent", "--show-error", "--fail", "--max-time", "2", "--unix-socket", r.Profile.Socket, "http://localhost/_/healthz"); err != nil {
		return fmt.Errorf("edge health check failed: %w", err)
	}
	return nil
}

func (r Runner) waitHealth(ctx context.Context) error {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if err := r.health(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("edge admin socket did not become ready")
		case <-tick.C:
		}
	}
}

func (r Runner) waitReady(ctx context.Context) error {
	url := "http://localhost/_/api/connectors/" + r.Profile.Peer + "/ready"
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		var body strings.Builder
		if r.run(ctx, nil, &body, "/usr/bin/curl", "--silent", "--show-error", "--fail", "--max-time", "2", "--unix-socket", r.Profile.Socket, url) == nil && strings.Contains(body.String(), `"ready":true`) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("paired connector did not become ready")
		case <-tick.C:
		}
	}
}

func fileHash(filename string) (string, error) {
	b, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return digest(b), nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeExecutable(data []byte, dst string) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
