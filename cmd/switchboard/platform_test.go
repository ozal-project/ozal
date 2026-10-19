package main

import (
	"runtime"
	"testing"
)

// skipOnWindows marks a test whose fixtures are POSIX absolute paths; see the
// twin helper in internal/config. The test still runs on Linux and macOS.
func skipOnWindows(t *testing.T, why string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows: " + why)
	}
}

// hasUnixPermissions reports whether file mode bits mean anything: Windows
// reports 0666 for every file.
func hasUnixPermissions() bool { return runtime.GOOS != "windows" }
