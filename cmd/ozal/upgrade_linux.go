//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/diskspace"
)

// upgradeSystem is the host as internal/upgrade needs it: systemctl, the
// service account and the admin listener.
type upgradeSystem struct {
	unit     string
	admin    string
	uid, gid int // -1 without a service account
	out      io.Writer
	pid      func(context.Context) (int, error) // tests substitute systemd, not /proc
}

func newUpgradeSystem(unit, account, admin string, out io.Writer) (*upgradeSystem, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("run upgrade as root (sudo): it replaces a system binary and restarts its unit")
	}
	// A dropped SSH session must not stop an upgrade halfway: SIGHUP would
	// kill the process, and a write to the vanished terminal would end it
	// with SIGPIPE unless the program asks for that signal itself.
	signal.Ignore(syscall.SIGHUP)
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	s := &upgradeSystem{unit: unit, admin: admin, uid: -1, gid: -1, out: out}
	if account != "" {
		u, err := user.Lookup(account)
		if err != nil {
			return nil, fmt.Errorf("service account: %w", err)
		}
		if s.uid, err = strconv.Atoi(u.Uid); err != nil {
			return nil, err
		}
		if s.gid, err = strconv.Atoi(u.Gid); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *upgradeSystem) Systemctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	cmd.Stdout, cmd.Stderr = s.out, s.out
	return cmd.Run()
}

func (s *upgradeSystem) Healthy(ctx context.Context) bool {
	if exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", s.unit).Run() != nil {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.admin, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *upgradeSystem) CheckSpace(ctx context.Context, needs ...diskspace.Need) error {
	return diskspace.Check(ctx, needs...)
}

func (s *upgradeSystem) mainPID(ctx context.Context) (int, error) {
	if s.pid != nil {
		return s.pid(ctx)
	}
	raw, err := exec.CommandContext(ctx, "systemctl", "show", "--property=MainPID", "--value", s.unit).Output()
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, errors.New("the unit has no live MainPID")
	}
	return pid, nil
}

// Running binds success to systemd's process image. Reading Binary on disk or
// a version string from another listener would not prove which build runs.
func (s *upgradeSystem) Running(ctx context.Context, want string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pid, err := s.mainPID(ctx)
	if err != nil || pid <= 0 {
		return false
	}
	path := "/proc/" + strconv.Itoa(pid) + "/exe"
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return false
	}
	got, err := imageSHA256(ctx, f)
	if err != nil || got != want {
		return false
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		return false
	}
	lastPID, err := s.mainPID(ctx)
	return err == nil && lastPID == pid && ctx.Err() == nil
}

// A reviewed version may have reached disk through another job before this
// confirmation. Treat it as a successful no-op only when the unit really runs
// that image; metadata-only checks deliberately do not query the host process.
func checkRunningInstalled(ctx context.Context, binary, unit, configPath, expectedHash string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("verify current process config: %w", err)
	}
	got, err := installedImageHash(ctx, binary)
	if err != nil {
		return err
	}
	if expectedHash != "" && got != expectedHash {
		return errors.New("the installed file does not match the expected update image")
	}
	sys := &upgradeSystem{unit: unit, admin: "http://" + cfg.Admin.Listen + "/_/healthz"}
	if !sys.Healthy(ctx) || !sys.Running(ctx, got) {
		return errors.New("the installed and active unit process image could not be verified")
	}
	return nil
}

// Run executes a command; asService drops to the service account, so a
// database the command creates or migrates stays that account's.
func (s *upgradeSystem) Run(ctx context.Context, asService bool, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = s.out, s.out
	cmd.Dir = "/"
	if asService {
		if s.uid < 0 {
			return errors.New("no service account to run as")
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
			Uid: uint32(s.uid), Gid: uint32(s.gid), Groups: []uint32{},
		}}
	}
	return cmd.Run()
}

func (s *upgradeSystem) Chown(path string) error {
	if s.uid < 0 {
		return nil
	}
	return os.Chown(path, s.uid, s.gid)
}
