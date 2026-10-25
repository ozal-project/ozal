package pages

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// entry is one tar header for a hand-built archive; a regular file's size
// is its body length.
type entry struct {
	name string
	flag byte
	body string
	link string
}

func archive(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		flag := e.flag
		if flag == 0 {
			flag = tar.TypeReg
		}
		size := int64(len(e.body))
		if flag != tar.TypeReg {
			size = 0
		}
		hdr := &tar.Header{Name: e.name, Typeflag: flag, Size: size, Mode: 0o644, Linkname: e.link}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if flag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtractWritesASite(t *testing.T) {
	dir := t.TempDir()
	m, err := Extract(archive(t,
		entry{name: "./", flag: tar.TypeDir},
		entry{name: "./index.html", body: "<h1>hi</h1>"},
		entry{name: "./assets/", flag: tar.TypeDir},
		entry{name: "./assets/app.js", body: "console.log(1)"},
		entry{name: "deep/er/page.html", body: "x"}, // parent directories created on demand
	), dir, DefaultLimits)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(m.Files) != 3 || m.Bytes != int64(len("<h1>hi</h1>")+len("console.log(1)")+1) {
		t.Fatalf("manifest = %+v", m)
	}
	got, err := os.ReadFile(filepath.Join(dir, "assets", "app.js"))
	if err != nil || string(got) != "console.log(1)" {
		t.Fatalf("assets/app.js = %q, %v", got, err)
	}
}

func TestExtractRefusesHostileArchives(t *testing.T) {
	small := Limits{MaxFileBytes: 64, MaxTotalBytes: 100, MaxEntries: 4, MaxDepth: 8}
	index := entry{name: "index.html", body: "ok"}
	for _, tc := range []struct {
		name    string
		entries []entry
		lim     Limits
		want    Code
	}{
		{"zip slip", []entry{index, {name: "../escaped.txt", body: "pwned"}}, DefaultLimits, UnsafePath},
		{"absolute path", []entry{index, {name: "/tmp/escaped.txt", body: "pwned"}}, DefaultLimits, UnsafePath},
		{"env file shipped by a build", []entry{index, {name: "./.env.production", body: "API_TOKEN=x"}}, DefaultLimits, SecretFile},
		// The classic two-step escape: a link, then a file "inside" it.
		{"symlink then file through it", []entry{index, {name: "assets", flag: tar.TypeSymlink, link: "/"}, {name: "assets/etc/x", body: "pwned"}}, DefaultLimits, Link},
		{"hard link", []entry{index, {name: "copy", flag: tar.TypeLink, link: "index.html"}}, DefaultLimits, Link},
		{"fifo", []entry{index, {name: "pipe", flag: tar.TypeFifo}}, DefaultLimits, Type},
		{"one file over the limit", []entry{index, {name: "big.bin", body: strings.Repeat("a", 65)}}, small, TooLarge},
		{"total over the limit", []entry{index, {name: "a", body: strings.Repeat("a", 60)}, {name: "b", body: strings.Repeat("b", 60)}}, small, TotalTooLarge},
		{"too many entries", []entry{index, {name: "a"}, {name: "b"}, {name: "c"}, {name: "d"}}, small, TooManyEntries},
		{"same path twice", []entry{index, {name: "./index.html", body: "replaced"}}, DefaultLimits, Duplicate},
		{"no index at the root", []entry{{name: "sub/index.html", body: "x"}}, DefaultLimits, NoIndex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "site")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			_, err := Extract(archive(t, tc.entries...), dir, tc.lim)
			var refusal *RefusedError
			if !errors.As(err, &refusal) || refusal.Code != tc.want {
				t.Fatalf("Extract error = %v, want refusal %s", err, tc.want)
			}
			// Nothing may ever appear next to the site directory.
			siblings, _ := os.ReadDir(parent)
			if len(siblings) != 1 {
				t.Fatalf("extraction wrote outside the site: %v", siblings)
			}
			if _, err := os.Stat(filepath.Join(os.TempDir(), "escaped.txt")); err == nil && tc.name == "absolute path" {
				t.Fatal("absolute path was written")
			}
		})
	}
}

func TestExtractRefusesGarbage(t *testing.T) {
	for name, body := range map[string][]byte{
		"not gzip":         []byte("PK\x03\x04 a zip file"),
		"gzip but not tar": gzipped(t, []byte(strings.Repeat("not a tar header ", 64))),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Extract(bytes.NewReader(body), t.TempDir(), DefaultLimits)
			var refusal *RefusedError
			if !errors.As(err, &refusal) || refusal.Code != BadArchive {
				t.Fatalf("Extract error = %v, want %s", err, BadArchive)
			}
		})
	}
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestManifestHashIgnoresOrder(t *testing.T) {
	a := []File{{Path: "index.html", SHA256: "aa"}, {Path: "app.js", SHA256: "bb"}}
	b := []File{{Path: "app.js", SHA256: "bb"}, {Path: "index.html", SHA256: "aa"}}
	if ManifestHash(a) != ManifestHash(b) {
		t.Fatal("the same files in another order must give the same ID")
	}
	c := []File{{Path: "index.html", SHA256: "aa"}, {Path: "app.js", SHA256: "cc"}}
	if ManifestHash(a) == ManifestHash(c) {
		t.Fatal("different content must give a different ID")
	}
	// Moving content between names is a different site.
	d := []File{{Path: "index.html", SHA256: "bb"}, {Path: "app.js", SHA256: "aa"}}
	if ManifestHash(a) == ManifestHash(d) {
		t.Fatal("swapping contents between paths must change the ID")
	}
}
