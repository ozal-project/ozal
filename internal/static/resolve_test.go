package static

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		urlPath string
		want    string
		ok      bool
	}{
		{"/", ".", true},
		{"/index.html", "index.html", true},
		{"/docs/", "docs", true},
		{"/docs/guide.html", "docs/guide.html", true},
		{"//double//slashes/", "double/slashes", true},
		{"/.well-known/security.txt", ".well-known/security.txt", true},

		// ".." never climbs above the root; it is resolved like a browser would.
		{"/../etc/passwd", "etc/passwd", true},
		{"/a/../../../etc/passwd", "etc/passwd", true},
		{"/..", ".", true},

		// Never reach the filesystem.
		{"", "", false},
		{"index.html", "", false},
		{"../etc/passwd", "", false},
		{`/..\..\windows\win.ini`, "", false},
		{"/index.html\x00.png", "", false},
		{"/.env", "", false},
		{"/.git/config", "", false},
		{"/assets/.htpasswd", "", false},
		{"/a/../.git/HEAD", "", false},

		// Pages control files configure the host and are never content, but
		// only at the site root: elsewhere they are ordinary files.
		{"/_headers", "", false},
		{"/_redirects", "", false},
		{"/./_headers", "", false},
		{"/a/../_redirects", "", false},
		{"/docs/_headers", "docs/_headers", true},
		{"/_headers.txt", "_headers.txt", true},
	}
	for _, tt := range tests {
		got, ok := Resolve(tt.urlPath)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Resolve(%q) = (%q, %v), want (%q, %v)", tt.urlPath, got, ok, tt.want, tt.ok)
		}
	}
}

// FuzzResolve checks the invariant every caller relies on: an accepted name is
// a valid io/fs path that cannot point above the root or at a hidden file.
func FuzzResolve(f *testing.F) {
	for _, seed := range []string{"/", "/a/b", "/../x", "/a/../../b", `/a\b`, "/.git", "/.well-known/x", "//x", "/_headers", "/_redirects"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, urlPath string) {
		name, ok := Resolve(urlPath)
		if !ok {
			return
		}
		if !fs.ValidPath(name) {
			t.Fatalf("Resolve(%q) = %q, not a valid fs path", urlPath, name)
		}
		for segment := range strings.SplitSeq(name, "/") {
			if slices.Contains(controlFiles, name) {
				t.Fatalf("Resolve(%q) = %q, exposes a Pages control file", urlPath, name)
			}
			if segment == ".." || (strings.HasPrefix(segment, ".") && segment != "." && segment != wellKnown) {
				t.Fatalf("Resolve(%q) = %q, contains forbidden segment %q", urlPath, name, segment)
			}
		}
	})
}
