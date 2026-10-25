//go:build windows

package audit

import "os"

func acquireDirectoryLock(string) (*os.File, error) {
	return nil, ErrUnsupportedPlatform
}

func acquireDirectoryReadLock(string) (*os.File, error) {
	return nil, ErrUnsupportedPlatform
}

func releaseDirectoryLock(*os.File) error { return nil }
