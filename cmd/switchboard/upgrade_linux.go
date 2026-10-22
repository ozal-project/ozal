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
	"syscall"
	"time"
)

// upgradeSystem is the host as internal/upgrade needs it: systemctl, the
// service account and the admin listener.
type upgradeSystem struct {
	unit     string
	admin    string
	uid, gid int // -1 without a service account
	out      io.Writer
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
	resp, err := client.Get(s.admin)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
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
