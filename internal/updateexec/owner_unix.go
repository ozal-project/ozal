//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package updateexec

import (
	"os"
	"syscall"
)

// ownedByRoot is checked only when the executor itself runs as root. The
// profile's mode bits stop group/other writes; ownership stops the service
// account (the file owner) from replacing the root binary.
func ownedByRoot(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0
}
