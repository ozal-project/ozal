package pages

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A copy serves the same files under the new name, with the same content
// address, and leaves the source site live and unchanged.
func TestCopyLiveServesTheSameFilesUnderANewName(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, migratedDB(t), t.TempDir(), 5)
	src, err := s.Deploy(ctx, "old-site", bytes.NewReader(archive(t,
		entry{name: "index.html", body: "<h1>Ozal</h1>"},
		entry{name: "assets/app.css", body: "body{}"},
		entry{name: "_headers", body: "/*\n  X-Test: yes\n"},
	).Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	copied, err := s.CopyLive(ctx, "old-site", "ozal-site")
	if err != nil {
		t.Fatal(err)
	}
	if copied.ID != src.ID || copied.Files != src.Files || copied.Site != "ozal-site" {
		t.Fatalf("copy = %+v, source = %+v", copied, src)
	}
	if liveIndex(t, s, "ozal-site") != "<h1>Ozal</h1>" || liveIndex(t, s, "old-site") != "<h1>Ozal</h1>" {
		t.Fatal("content differs after the copy")
	}
	live, _ := s.Live("ozal-site")
	if css, err := os.ReadFile(filepath.Join(live.Dir, "assets", "app.css")); err != nil || string(css) != "body{}" {
		t.Fatalf("nested file: %q, %v", css, err)
	}
	if live.Rules == nil {
		t.Fatal("the copy lost its _headers rules")
	}
	if _, err := s.CopyLive(ctx, "nothing-here", "x"); !errors.Is(err, ErrNothingLive) {
		t.Fatalf("copy of a site without a live deployment: %v", err)
	}
	if _, err := s.CopyLive(ctx, "old-site", "Bad Name"); !errors.Is(err, ErrBadSiteName) {
		t.Fatalf("bad target name: %v", err)
	}
}
