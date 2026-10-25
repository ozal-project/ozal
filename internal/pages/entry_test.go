package pages

import (
	"strings"
	"testing"
)

func TestCheckEntry(t *testing.T) {
	lim := Limits{MaxFileBytes: 1000, MaxTotalBytes: 5000, MaxEntries: 10, MaxDepth: 4}

	for _, tc := range []struct {
		name  string
		entry string
		kind  Kind
		size  int64
		want  Code
		path  string // expected cleaned path when allowed
	}{
		// What real build tools produce.
		{"plain file", "index.html", KindFile, 10, EntryAllowed, "index.html"},
		{"nested file", "assets/app.js", KindFile, 10, EntryAllowed, "assets/app.js"},
		{"dot-slash prefix from tar -C dist .", "./index.html", KindFile, 10, EntryAllowed, "index.html"},
		{"directory with trailing slash", "assets/", KindDir, 0, EntryAllowed, "assets"},
		{"root directory entry", "./", KindDir, 0, EntryAllowed, ""},
		{"root directory as dot", ".", KindDir, 0, EntryAllowed, ""},
		{"dot-slash-slash must not become absolute", ".//", KindDir, 0, EntryAllowed, ""},
		{"dotfile is just a name", ".well-known/security.txt", KindFile, 10, EntryAllowed, ".well-known/security.txt"},
		{"file of exactly the limit", "big.bin", KindFile, 1000, EntryAllowed, "big.bin"},
		{"depth at the limit", "a/b/c/d.txt", KindFile, 1, EntryAllowed, "a/b/c/d.txt"},
		{"redundant slashes are cleaned", "assets//app.js", KindFile, 10, EntryAllowed, "assets/app.js"},

		// Traversal ("zip slip"): anything that could leave the site.
		{"parent escape", "../evil", KindFile, 1, UnsafePath, ""},
		{"deep parent escape", "../../etc/cron.d/x", KindFile, 1, UnsafePath, ""},
		{"escape in the middle", "assets/../../evil", KindFile, 1, UnsafePath, ""},
		{"dotdot that cleans to inside is still refused", "assets/../index.html", KindFile, 1, UnsafePath, ""},
		{"bare dotdot", "..", KindDir, 0, UnsafePath, ""},
		{"absolute unix path", "/etc/passwd", KindFile, 1, UnsafePath, ""},
		{"windows drive letter", "C:/Windows/evil.dll", KindFile, 1, UnsafePath, ""},
		{"backslash separator", `assets\..\..\evil`, KindFile, 1, UnsafePath, ""},
		{"unc-style path", `\\server\share\x`, KindFile, 1, UnsafePath, ""},
		{"NUL byte", "index.html\x00.png", KindFile, 1, UnsafePath, ""},
		{"empty name", "", KindFile, 1, UnsafePath, ""},

		// Entry types.
		{"symlink", "assets", KindSymlink, 0, Link, ""},
		{"hard link", "copy.html", KindHardlink, 0, Link, ""},
		{"device or fifo", "dev", KindOther, 0, Type, ""},

		// Size and shape.
		{"one byte over the file limit", "big.bin", KindFile, 1001, TooLarge, ""},
		{"negative size", "weird.bin", KindFile, -1, TooLarge, ""},
		{"deeper than the limit", "a/b/c/d/e.txt", KindFile, 1, TooDeep, ""},

		// Names that only ever hold secrets: refused wherever they appear.
		{"env file at the root", ".env", KindFile, 1, SecretFile, ""},
		{"env variant", "./.env.production", KindFile, 1, SecretFile, ""},
		{"env file in a subdirectory", "assets/.env.local", KindFile, 1, SecretFile, ""},
		{"env in another case", ".ENV", KindFile, 1, SecretFile, ""},
		{"git directory", ".git/", KindDir, 0, SecretFile, ""},
		{"file inside .git", ".git/config", KindFile, 1, SecretFile, ""},
		{"npm credentials", ".npmrc", KindFile, 1, SecretFile, ""},
		{"private SSH key", "keys/id_ed25519", KindFile, 1, SecretFile, ""},
		{"public SSH key is fine", "keys/id_ed25519.pub", KindFile, 1, EntryAllowed, "keys/id_ed25519.pub"},
		{"a name that only contains env", "environment.html", KindFile, 1, EntryAllowed, "environment.html"},
		{"dotenv-like page is fine", "docs/env.html", KindFile, 1, EntryAllowed, "docs/env.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckEntry(tc.entry, tc.kind, tc.size, lim)
			if got.Code != tc.want || got.Allowed != (tc.want == EntryAllowed) {
				t.Fatalf("CheckEntry(%q) = %+v, want code %s", tc.entry, got, tc.want)
			}
			if got.Allowed && got.Path != tc.path {
				t.Fatalf("CheckEntry(%q).Path = %q, want %q", tc.entry, got.Path, tc.path)
			}
			if !got.Allowed && got.Path != "" {
				t.Fatalf("a refused entry must not carry a path, got %q", got.Path)
			}
			if got.Reason == "" {
				t.Fatal("every decision needs a reason")
			}
			if tc.entry != "" && strings.Contains(got.Reason, tc.entry) {
				t.Fatalf("the reason echoes the attacker-controlled name: %q", got.Reason)
			}
		})
	}
}

// FuzzCheckEntry states the invariant directly: whatever the name, an allowed
// path is relative, slash-separated and has no ".." component.
func FuzzCheckEntry(f *testing.F) {
	for _, seed := range []string{"index.html", "./a/b", "../x", "/abs", `a\b`, "a/../b", "C:/x", "a//b/", ".", "a/.env", ".git/HEAD", "./.Env.local"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		got := CheckEntry(name, KindFile, 1, DefaultLimits)
		if !got.Allowed {
			return
		}
		if strings.HasPrefix(got.Path, "/") || strings.Contains(got.Path, `\`) || strings.ContainsRune(got.Path, 0) {
			t.Fatalf("allowed unsafe path %q from %q", got.Path, name)
		}
		for part := range strings.SplitSeq(got.Path, "/") {
			if part == ".." {
				t.Fatalf("allowed a .. component in %q from %q", got.Path, name)
			}
			if lower := strings.ToLower(part); lower == ".env" || strings.HasPrefix(lower, ".env.") || lower == ".git" {
				t.Fatalf("allowed a secret file component in %q from %q", got.Path, name)
			}
		}
	})
}
