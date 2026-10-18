//go:build !unix

package certs

import (
	"errors"
	"os"
)

// acquireLock is the portable fallback: an exclusively created lock file.
// It rejects a concurrent writer like flock does, but unlike flock it is not
// released by process exit — after an unclean shutdown on a platform without
// flock, remove the stale lock file manually. Deployment targets Linux, so
// the flock implementation is the one that matters in production.
func acquireLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, stateFileMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, errors.New("lock file exists")
		}
		return nil, err
	}
	file.Close()
	return func() { os.Remove(path) }, nil
}

// syncDirectory is a no-op on platforms where directory fsync is not
// available.
func syncDirectory(string) error { return nil }
