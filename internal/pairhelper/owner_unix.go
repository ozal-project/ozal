//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package pairhelper

import (
	"os"
	"syscall"
)

// OwnedByRoot reports whether a root-trusted input file belongs to root, so the
// account that requests helper actions cannot have replaced it.
func OwnedByRoot(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0
}
