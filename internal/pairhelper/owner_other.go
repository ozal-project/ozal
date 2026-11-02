//go:build !(linux || darwin || freebsd || openbsd || netbsd || dragonfly)

package pairhelper

import "os"

// OwnedByRoot fails closed where file ownership cannot be read; the helper
// runs only on Unix edges.
func OwnedByRoot(os.FileInfo) bool { return false }
