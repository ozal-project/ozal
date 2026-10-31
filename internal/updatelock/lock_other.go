//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package updatelock

import (
	"errors"
	"os"
)

// flaw on unsupported platforms: the installer and the upgrade are Linux
// features, so an update must fail closed rather than run without exclusion.
func lockFile(*os.File) error {
	return errors.New("update locking is not supported on this platform")
}
