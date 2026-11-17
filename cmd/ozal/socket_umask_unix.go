//go:build unix

package main

import "syscall"

// privateSocketUmask narrows the process umask while a unix socket is bound.
// The socket's inode takes its mode from the umask, so binding it under the
// default 022 leaves a short window where any local user can connect before
// the explicit chmod 0600. The caller runs this during startup and restores
// the old mask as soon as the bind returns.
func privateSocketUmask() func() {
	old := syscall.Umask(0o177)
	return func() { syscall.Umask(old) }
}
