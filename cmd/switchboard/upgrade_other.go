//go:build !linux

package main

import (
	"errors"
	"io"

	"github.com/switchboard-project/switchboard/internal/upgrade"
)

func newUpgradeSystem(_, _, _ string, _ io.Writer) (upgrade.System, error) {
	return nil, errors.New("upgrade needs Linux and systemd")
}
