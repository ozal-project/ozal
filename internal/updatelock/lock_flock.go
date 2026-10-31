//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package updatelock

import (
	"errors"
	"os"
	"syscall"
)

func lockFile(f *os.File) error {
	// An adopted descriptor must not leak to migration or systemctl children.
	// Only an explicit ExtraFiles transfer may extend ownership.
	syscall.CloseOnExec(int(f.Fd()))
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return ErrBusy
	}
	return err
}
