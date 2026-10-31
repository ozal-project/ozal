//go:build linux

package diskspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeCapacityProbeRefusesAnUnavailableFilesystem(t *testing.T) {
	dir := t.TempDir()
	if _, err := probe(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := Check(context.Background(), Need{Dir: filepath.Join(dir, "missing"), Bytes: 1}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing filesystem err=%v", err)
	}
}
