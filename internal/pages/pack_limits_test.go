package pages

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackRefusesOversizedSparseSourceBeforeReading(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(DefaultLimits.MaxFileBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	if _, err := Pack(dir); err == nil {
		t.Fatal("packed oversized source")
	}
}
