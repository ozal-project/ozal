//go:build unix

package main

import (
	"os"
	"syscall"
)

// sameOwner gives path the owner of like, when running as root.
func sameOwner(path string, like os.FileInfo) error {
	stat, ok := like.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 {
		return nil
	}
	return os.Chown(path, int(stat.Uid), int(stat.Gid))
}

// sameOwnerInRoot changes ownership inside the pinned state directory,
// without following a final symbolic link.
func sameOwnerInRoot(root *os.Root, path string, like os.FileInfo) error {
	stat, ok := like.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 {
		return nil
	}
	return root.Lchown(path, int(stat.Uid), int(stat.Gid))
}
