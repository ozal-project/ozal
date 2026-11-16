//go:build !unix

package install

import "os"

func chownLike(*os.File, os.FileInfo) error { return nil }
