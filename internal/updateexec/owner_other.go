//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package updateexec

import "os"

// The executor is a Linux systemd component; other builds do not run it.
func ownedByRoot(os.FileInfo) bool { return true }
