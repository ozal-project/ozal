//go:build !linux

package main

import (
	"context"
	"errors"
	"io"

	"github.com/ozal-project/ozal/internal/upgrade"
)

func checkRunningInstalled(context.Context, string, string, string, string) error {
	return errors.New("verifying the active update process needs Linux and systemd")
}

func newUpgradeSystem(_, _, _ string, _ io.Writer) (upgrade.System, error) {
	return nil, errors.New("upgrade needs Linux and systemd")
}
