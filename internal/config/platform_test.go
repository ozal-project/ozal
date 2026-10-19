package config

import (
	"runtime"
	"testing"
)

// skipOnWindows marks a test whose fixtures are POSIX absolute paths
// ("/var/lib/..."). filepath.IsAbs is false for those on Windows, which is
// correct product behavior, so the fixtures cannot be reused there. The same
// tests run on Linux and macOS; Windows keeps covering parsing everywhere else.
func skipOnWindows(t *testing.T, why string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows: " + why)
	}
}

// hasUnixPermissions reports whether file mode bits mean anything: Windows
// reports 0666 for every file, so 0600/0640 cannot be asserted there.
func hasUnixPermissions() bool { return runtime.GOOS != "windows" }
