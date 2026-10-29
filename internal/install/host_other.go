//go:build !linux

package install

import (
	"context"
	"errors"
)

type LinuxHost struct{}

func (LinuxHost) Account(context.Context) (Account, error) {
	return Account{}, errors.New("systemd installation requires Linux")
}
func (LinuxHost) RunAs(context.Context, Account, string, ...string) error {
	return errors.New("systemd installation requires Linux")
}
func (LinuxHost) Systemctl(context.Context, ...string) error {
	return errors.New("systemd installation requires Linux")
}
func (LinuxHost) Healthy(context.Context) bool { return false }
