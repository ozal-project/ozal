//go:build linux

package install

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
	"time"
)

type LinuxHost struct{}

func (LinuxHost) Account(ctx context.Context) (Account, error) {
	account, err := user.Lookup("ozal")
	if err != nil {
		var unknown user.UnknownUserError
		if !errors.As(err, &unknown) {
			return Account{}, errors.New("cannot inspect service account")
		}
		command := exec.CommandContext(ctx, "useradd", "--system", "--user-group", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", "ozal")
		if command.Run() != nil {
			return Account{}, errors.New("cannot create unprivileged service account")
		}
		account, err = user.Lookup("ozal")
		if err != nil {
			return Account{}, errors.New("cannot inspect new service account")
		}
	}
	uid, e1 := strconv.ParseUint(account.Uid, 10, 32)
	gid, e2 := strconv.ParseUint(account.Gid, 10, 32)
	if e1 != nil || e2 != nil || uid == 0 || gid == 0 {
		return Account{}, errors.New("service account must not be privileged")
	}
	return Account{UID: int(uid), GID: int(gid)}, nil
}

func (LinuxHost) RunAs(ctx context.Context, account Account, binary string, args ...string) error {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = "/var/lib/ozal"
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(account.UID), Gid: uint32(account.GID), Groups: []uint32{uint32(account.GID)}}}
	if command.Run() != nil {
		return errors.New("service-state initialization failed; inspect the installation locally")
	}
	return nil
}

func (LinuxHost) Systemctl(ctx context.Context, args ...string) error {
	if exec.CommandContext(ctx, "systemctl", args...).Run() != nil {
		return errors.New("systemd operation failed")
	}
	return nil
}

func (LinuxHost) Healthy(ctx context.Context) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:9090/_/healthz", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == 200
}
