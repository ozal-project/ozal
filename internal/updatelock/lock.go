// Package updatelock serializes every process that may replace the installed
// binary, migrate its database or use its .next/.bak files. The lock is an
// advisory flock held for the whole update, not only while copying files.
package updatelock

import (
	"errors"
	"fmt"
	"os"
)

// ErrBusy means another update owns the lock. The caller must not retry by
// force; it should report the running update and let it finish.
var ErrBusy = errors.New("another update is already running")

// EnvFD identifies the inherited lock descriptor passed in exec.Cmd.ExtraFiles.
// A child validates it against its lock path before touching the installation.
const EnvFD = "OZAL_UPGRADE_LOCK_FD"

// Lock is one held update lock.
type Lock struct {
	file *os.File
}

// Acquire takes the lock without blocking. A stale file is harmless: the
// kernel releases the flock when its owner exits.
func Acquire(path string) (*Lock, error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("update lock %s is not a regular file", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect the update lock: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the update lock: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, ErrBusy) {
			return nil, fmt.Errorf("%w (lock %s)", ErrBusy, path)
		}
		return nil, fmt.Errorf("take the update lock: %w", err)
	}
	return &Lock{file: f}, nil
}

// File is passed in exec.Cmd.ExtraFiles to keep the same lock alive in an
// upgrade child. The caller must not close it; Release owns that operation.
func (l *Lock) File() *os.File { return l.file }

// Inherit adopts a child descriptor for the same regular lock file. Taking
// flock again on the inherited open file description preserves the lock; an
// unrelated, unlocked descriptor cannot bypass exclusion.
func Inherit(path string, fd uintptr) (*Lock, error) {
	if fd < 3 {
		return nil, errors.New("the inherited update lock must not be a standard stream")
	}
	f := os.NewFile(fd, "inherited update lock")
	if f == nil {
		return nil, errors.New("the inherited update lock descriptor is invalid")
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("inspect the inherited update lock: %w", err)
	}
	want, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !want.Mode().IsRegular() || !os.SameFile(info, want) {
		f.Close()
		return nil, errors.New("the inherited update lock does not match the regular lock file")
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("take the inherited update lock: %w", err)
	}
	return &Lock{file: f}, nil
}

// Release closes this process's descriptor. Do not issue LOCK_UN: that would
// unlock the shared open file description even while an upgrade child is
// still using it. The kernel releases the lock when the last descriptor closes.
// The file stays in place to avoid an unlink race between processes.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
