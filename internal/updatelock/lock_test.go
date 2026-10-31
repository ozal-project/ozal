package updatelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireRefusesASecondUpdateUntilRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrBusy) {
		t.Fatalf("second lock err = %v, want ErrBusy", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	second.Release()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode: %v %v", info, err)
	}
}

func TestAcquireRefusesANonRegularLockPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(link); err == nil {
		t.Fatal("symlinked lock accepted")
	}
}
