//go:build !windows

package audit

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func acquireDirectoryLock(path string) (*os.File, error) {
	return acquireLock(path, syscall.LOCK_EX)
}

func acquireDirectoryReadLock(path string) (*os.File, error) {
	return acquireLock(path, syscall.LOCK_SH)
}

func acquireLock(path string, kind int) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("audit lock must be a regular file")
	}
	if err == nil && info.Mode().Perm()&0o077 != 0 {
		err = errors.New("audit lock permissions are broader than 0600")
	}
	if err == nil {
		err = syscall.Flock(fd, kind|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			err = ErrDirectoryLocked
		}
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func releaseDirectoryLock(file *os.File) error {
	if file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil || closeErr != nil {
		return fmt.Errorf("release audit directory lock: %w", errors.Join(unlockErr, closeErr))
	}
	return nil
}
