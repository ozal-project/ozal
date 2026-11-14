//go:build unix

package config

import (
	"os"
	"syscall"
)

// keepOwner gives the replacement the owner and group of the file it
// replaces. Only root can do that, and only root needs to: a root command
// that rewrites a configuration the service user reads (root:ozal, 0640)
// must not leave it unreadable to the service.
func keepOwner(tmp *os.File, previous os.FileInfo) error {
	if previous == nil || os.Geteuid() != 0 {
		return nil
	}
	stat, ok := previous.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return tmp.Chown(int(stat.Uid), int(stat.Gid))
}
