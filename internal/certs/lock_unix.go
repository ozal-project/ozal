//go:build unix

package certs

import (
	"os"
	"syscall"
)

// acquireLock takes an exclusive advisory lock on path. The lock is released
// by the returned function or by process exit, so a crash never leaves a
// stale lock behind.
func acquireLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, stateFileMode)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return func() { file.Close() }, nil
}

// syncDirectory makes a rename or a new directory entry durable.
func syncDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
