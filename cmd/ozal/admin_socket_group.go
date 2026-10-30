//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// shareAdminSocket opens the admin socket to one system group (mode 0660),
// so its members reach the host operator's API without sudo. It fails
// closed: a missing group, a service account outside it, or a parent
// directory the group cannot traverse stops startup instead of leaving a
// socket that looks shared but is not.
func shareAdminSocket(path, group string) error {
	if group == "" {
		return nil
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return fmt.Errorf("admin.socket_group %q: %w", group, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("admin.socket_group %q has a non-numeric gid", group)
	}
	if err := os.Chown(path, -1, gid); err != nil {
		return fmt.Errorf("admin.socket_group %q: the service account must belong to it (systemd SupplementaryGroups=%s): %w", group, group, err)
	}
	if err := os.Chmod(path, 0o660); err != nil {
		return err
	}
	return traversable(filepath.Dir(path), gid)
}

// traversable reports whether members of gid can reach entries of dir: the
// directory must grant search to its group (when that is gid) or to others.
func traversable(dir string, gid int) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode&0o001 != 0 {
		return nil
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Gid) == gid && mode&0o010 != 0 {
		return nil
	}
	return errors.New("admin.socket_group: the socket's directory " + dir + " is closed to the group; set RuntimeDirectoryMode=0711 (systemd) or give the directory to the group")
}
