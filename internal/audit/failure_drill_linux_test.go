//go:build linux

package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/ozal-project/ozal/internal/events"
)

// /dev/full supplies a real ENOSPC without filling a shared filesystem.
func TestDiskFullFailsClosedAndPreservesEarlierRecords(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	w, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Append(ctx, events.Event{Type: events.AccessAllowed}); err != nil {
		t.Fatal(err)
	}
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	if err := w.file.Close(); err != nil {
		w.mu.Unlock()
		t.Fatal(err)
	}
	w.file = full
	w.mu.Unlock()
	if err := w.Append(ctx, events.Event{Type: events.AccessDenied}); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("append: %v, want ENOSPC", err)
	}
	if err := w.Append(ctx, events.Event{Type: events.AccessDenied}); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("later append: %v, want unavailable with ENOSPC", err)
	}
	if err := w.Close(); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("close: %v, want ENOSPC", err)
	}
	v, err := VerifyDir(ctx, dir)
	if err != nil || v.Records != 1 {
		t.Fatalf("verify: %+v, %v", v, err)
	}
	reopened, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Append(ctx, events.Event{Type: events.AccessDenied}); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	v, err = VerifyDir(ctx, dir)
	if err != nil || v.Records != 2 {
		t.Fatalf("verify after recovery: %+v, %v", v, err)
	}
}
