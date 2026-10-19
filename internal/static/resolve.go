// Package static serves a site from a directory, the way Cloudflare Pages
// does, without letting a request reach anything outside that directory.
//
// Resolve is the pure core: it turns a URL path into a name relative to the
// site root, or refuses it. Handler is the shell: it opens names through
// os.OpenInRoot, so the kernel-level check still holds if Resolve ever lets
// something through.
package static

import (
	"io/fs"
	"path"
	"slices"
	"strings"
)

// wellKnown is the only dot-directory a site may publish (security.txt,
// ACME challenges, app links).
const wellKnown = ".well-known"

// controlFiles are the Cloudflare Pages files that configure a site (response
// headers and redirects). Like Pages, a site honours them at its root and never
// publishes them as content.
var controlFiles = []string{"_headers", "_redirects"}

// Resolve maps an already-decoded URL path to a name relative to the site
// root, in the form io/fs expects: "." for the root itself, no leading slash,
// forward slashes only. It reports false for paths that must never reach the
// filesystem: relative paths, backslashes, NUL bytes, hidden files such as
// .git or .env, and the root-level Pages control files.
//
// ".." segments cannot climb above the root: path.Clean resolves "/a/../.."
// to "/", the same way a browser would.
func Resolve(urlPath string) (string, bool) {
	if !strings.HasPrefix(urlPath, "/") || strings.ContainsAny(urlPath, "\\\x00") {
		return "", false
	}
	name := strings.TrimPrefix(path.Clean(urlPath), "/")
	if name == "" {
		return ".", true
	}
	for segment := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(segment, ".") && segment != wellKnown {
			return "", false
		}
	}
	if slices.Contains(controlFiles, name) {
		return "", false
	}
	return name, fs.ValidPath(name)
}
