// Package upgrade replaces the binary of a Switchboard systemd unit in place
// (REL-010): the procedure proven by hand on both edges on 2026-10-22,
// without any host's paths.
//
// The new binary upgrades to itself. Run as root, it copies itself next to
// the installed one and checks the copy's SHA-256 before running anything,
// so a file its owner swaps mid-run is never executed as root. It validates
// the config, stops the unit, migrates the state database as the service
// account (backing it up first when migrations are pending), keeps the
// previous binary, starts the unit and waits for the admin listener. Any
// failure after the stop puts back the previous binary and, when it
// migrated, the database, and starts the unit again.
package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Plan names what to replace and how to check it.
type Plan struct {
	Unit   string // systemd unit running Binary
	Binary string // installed binary, replaced in place
	Source string // the new binary: normally the running executable
	SHA256 string // expected SHA-256 of Source, from where it was built
	Config string // routing config the unit runs with
	DB     string // state database; empty when the unit runs without -db
	Keep   int    // previous binaries kept beside Binary

	Poll     time.Duration // between health checks (default 1s)
	Attempts int           // health checks before giving up (default 20)
	Now      func() time.Time
}

// System is what an upgrade does to the host, so tests can stand in for
// systemd, the service account and the admin listener.
type System interface {
	Systemctl(ctx context.Context, args ...string) error
	// Healthy reports whether the unit is active and its admin answers.
	Healthy(ctx context.Context) bool
	// Run executes a command, as the service account when asService.
	Run(ctx context.Context, asService bool, name string, args ...string) error
	// Chown gives a file root created to the service account.
	Chown(path string) error
}

// Result is how an upgrade ended; ExitCode maps it for scripts.
type Result int

const (
	Upgraded   Result = iota // the unit runs the new binary
	Refused                  // stopped before the unit was touched
	RolledBack               // failed; the previous version is back and healthy
	Down                     // failed; the previous version does not answer
)

func (r Result) ExitCode() int { return [...]int{0, 2, 3, 4}[r] }

// Run performs the upgrade. ctx can cancel it only before the unit is
// stopped; from then on it always finishes, rolling back if needed.
func Run(ctx context.Context, p Plan, sys System, out io.Writer) (Result, error) {
	p.defaults()
	next := p.Binary + ".next"
	step := func(format string, a ...any) { fmt.Fprintf(out, "==> "+format+"\n", a...) }

	step("copy %s next to %s and check its SHA-256", p.Source, p.Binary)
	if err := copyFile(p.Source, next, 0o755); err != nil {
		return Refused, err
	}
	got, err := fileSHA256(next)
	if err != nil || got != p.SHA256 {
		os.Remove(next)
		return Refused, fmt.Errorf("the new binary's SHA-256 is %.12s…, want %.12s…: refused", got, p.SHA256)
	}
	current, err := fileSHA256(p.Binary)
	if err != nil {
		os.Remove(next)
		return Refused, fmt.Errorf("read the installed binary: %w", err)
	}
	if current == got {
		os.Remove(next)
		step("%s already runs this binary: nothing to do", p.Unit)
		return Upgraded, nil
	}
	if err := sys.Run(ctx, false, next, "validate", "-config", p.Config); err != nil {
		os.Remove(next)
		return Refused, fmt.Errorf("the new binary refuses %s: %w", p.Config, err)
	}
	if err := ctx.Err(); err != nil {
		os.Remove(next)
		return Refused, err
	}

	// From the stop on, finish whatever happens to the caller or its session.
	ctx = context.WithoutCancel(ctx)
	r := rollback{p: p, sys: sys, out: out, next: next, previous: p.Binary + ".bak-" + current[:12]}

	step("stop %s (down until it starts again)", p.Unit)
	if err := sys.Systemctl(ctx, "stop", p.Unit); err != nil {
		return r.run(ctx, fmt.Errorf("stop: %w", err))
	}
	if p.DB != "" {
		backup := fmt.Sprintf("%s.pre-%.12s-%s", p.DB, got, p.Now().UTC().Format("20060102T150405Z"))
		step("migrate %s as the service account", p.DB)
		err := sys.Run(ctx, true, next, "migrate", "up", "-db", p.DB, "-backup", backup)
		if _, statErr := os.Lstat(backup); statErr == nil {
			r.dbBackup = backup
		}
		if err != nil {
			return r.run(ctx, fmt.Errorf("migrate: %w", err))
		}
	}
	step("swap binaries (previous kept as %s)", filepath.Base(r.previous))
	if err := copyFile(p.Binary, r.previous, 0o755); err != nil {
		return r.run(ctx, fmt.Errorf("keep the previous binary: %w", err))
	}
	if err := os.Rename(next, p.Binary); err != nil {
		return r.run(ctx, fmt.Errorf("swap: %w", err))
	}
	r.swapped = true
	step("start %s", p.Unit)
	sys.Systemctl(ctx, "reset-failed", p.Unit)
	if err := sys.Systemctl(ctx, "start", p.Unit); err != nil {
		return r.run(ctx, fmt.Errorf("start: %w", err))
	}
	if !p.healthy(ctx, sys) {
		return r.run(ctx, errors.New("health check: the unit is not active or its admin does not answer"))
	}
	if r.dbBackup != "" {
		step("database before the migrations kept as %s", r.dbBackup)
	}
	prune(p.Binary, p.Keep, out)
	step("%s runs the new binary", p.Unit)
	return Upgraded, nil
}

type rollback struct {
	p        Plan
	sys      System
	out      io.Writer
	next     string
	previous string
	dbBackup string
	swapped  bool
}

func (r rollback) run(ctx context.Context, cause error) (Result, error) {
	fmt.Fprintf(r.out, "==> %v: restoring the previous version\n", cause)
	r.sys.Systemctl(ctx, "stop", r.p.Unit)
	if r.swapped {
		if err := copyFile(r.previous, r.p.Binary, 0o755); err != nil {
			fmt.Fprintf(r.out, "    could not restore the binary: %v\n", err)
		}
	} else {
		os.Remove(r.next)
	}
	if r.dbBackup != "" {
		// The previous binary refuses a newer schema: the database goes back too.
		if err := r.restoreDB(); err != nil {
			fmt.Fprintf(r.out, "    could not restore the database: %v\n", err)
		}
	}
	r.sys.Systemctl(ctx, "reset-failed", r.p.Unit)
	r.sys.Systemctl(ctx, "start", r.p.Unit)
	if r.p.healthy(ctx, r.sys) {
		return RolledBack, cause
	}
	return Down, cause
}

// restoreDB puts the backup back as the service account's file. The WAL
// and shared-memory files of the migrated database go first: left behind,
// they would be replayed onto the restored one.
func (r rollback) restoreDB() error {
	db := r.p.DB
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(db + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := copyFile(r.dbBackup, db, 0o600); err != nil {
		return err
	}
	return r.sys.Chown(db)
}

func (p *Plan) defaults() {
	if p.Poll == 0 {
		p.Poll = time.Second
	}
	if p.Attempts == 0 {
		p.Attempts = 20
	}
	if p.Now == nil {
		p.Now = time.Now
	}
}

func (p Plan) healthy(ctx context.Context, sys System) bool {
	for i := 0; i < p.Attempts; i++ {
		if sys.Healthy(ctx) {
			return true
		}
		time.Sleep(p.Poll)
	}
	return false
}

// prune keeps the newest keep previous binaries; database backups are
// never removed here.
func prune(binary string, keep int, out io.Writer) {
	matches, _ := filepath.Glob(binary + ".bak-*")
	type entry struct {
		path string
		mod  time.Time
	}
	var entries []entry
	for _, m := range matches {
		if info, err := os.Lstat(m); err == nil && info.Mode().IsRegular() {
			entries = append(entries, entry{m, info.ModTime()})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })
	for _, e := range entries[min(keep, len(entries)):] {
		if os.Remove(e.path) == nil {
			fmt.Fprintf(out, "    removed the older %s\n", filepath.Base(e.path))
		}
	}
}

// copyFile writes src to dst through a temporary file renamed into place,
// so dst is either absent, the old file, or complete.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, in)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
