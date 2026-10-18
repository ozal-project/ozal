//go:build linux

package tunnelrenew

import (
	"path/filepath"
	"testing"
)

func TestRenewalLockRejectsConcurrentWriterAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	release, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := acquireLock(path); err == nil {
		other()
		release()
		t.Fatal("concurrent renewal acquired lock")
	}
	release()
	other, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	other()
}
