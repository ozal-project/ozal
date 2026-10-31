//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package updatelock

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReleaseKeepsInheritedLockUntilLastDescriptorCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.lock")
	parent, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Release()
	fd, err := syscall.Dup(int(parent.File().Fd()))
	if err != nil {
		t.Fatal(err)
	}
	child, err := Inherit(path, uintptr(fd))
	if err != nil {
		t.Fatal(err)
	}
	defer child.Release()
	if err := parent.Release(); err != nil {
		t.Fatal(err)
	}
	if other, err := Acquire(path); !errors.Is(err, ErrBusy) {
		if other != nil {
			other.Release()
		}
		t.Fatalf("parent closed with child still alive: err=%v, want ErrBusy", err)
	}
	if err := child.Release(); err != nil {
		t.Fatal(err)
	}
	other, err := Acquire(path)
	if err != nil {
		t.Fatalf("last descriptor closed but lock is still busy: %v", err)
	}
	other.Release()
}

func TestInheritRefusesAnotherFileAndAnUnrelatedOpenDescription(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update.lock")
	held, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	for _, tc := range []struct {
		name string
		path string
		busy bool
	}{
		{"another file", filepath.Join(dir, "other.lock"), false},
		{"same file without inherited ownership", path, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.OpenFile(tc.path, os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			fd, err := syscall.Dup(int(f.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			lock, err := Inherit(path, uintptr(fd))
			if lock != nil {
				lock.Release()
			}
			if err == nil || (tc.busy && !errors.Is(err, ErrBusy)) {
				t.Fatalf("err=%v, want refusal (busy=%v)", err, tc.busy)
			}
		})
	}
}
