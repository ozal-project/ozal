package static

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/octaviosantacruz/switchboard/internal/pages"
)

const indexFile = "index.html"

// Handler serves files from Dir. It never lists directories: a directory is
// served through its index.html. With SPA set, a missing extensionless path
// falls back to the root index.html so client-side routers work.
//
// Live, when set, replaces Dir: it is asked once per request for the live
// deployment (PAGES-002), so a deploy or rollback takes effect on the next
// request and a request never mixes two deployments. Its rules (PAGES-003)
// come with it: a matching _redirects rule answers before any file, and
// _headers rules are added to the files served.
type Handler struct {
	Dir  string
	Live func() (pages.Live, bool)
	SPA  bool
}

// ServeHTTP implements http.Handler.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name, ok := Resolve(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	dir := h.Dir
	var rules *pages.Rules
	if h.Live != nil {
		live, ok := h.Live()
		if !ok {
			http.Error(w, "this site has no deployment yet", http.StatusServiceUnavailable)
			return
		}
		dir, rules = live.Dir, live.Rules
	}
	// Like Cloudflare Pages, a redirect applies even where a file exists.
	if to, status, ok := rules.Redirect(r.URL.Path); ok {
		http.Redirect(w, r, to, status)
		return
	}

	file, info, err := open(dir, name)
	if err == nil && info.IsDir() {
		file.Close()
		if !strings.HasSuffix(r.URL.Path, "/") {
			// Built from the cleaned name, never from r.URL.Path: a raw
			// "//evil.example" would become a protocol-relative open redirect.
			redirectToDir(w, r, name)
			return
		}
		file, info, err = open(dir, path.Join(name, indexFile))
	}
	if errors.Is(err, fs.ErrNotExist) && h.SPA && path.Ext(name) == "" {
		file, info, err = open(dir, indexFile)
	}
	if err != nil || info.IsDir() {
		if file != nil {
			file.Close()
		}
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	for _, header := range rules.Headers(r.URL.Path) {
		w.Header().Set(header.Name, header.Value)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

// open opens name inside dir. os.OpenInRoot fails if name, or any symlink it
// follows, would leave dir.
func open(dir, name string) (*os.File, fs.FileInfo, error) {
	file, err := os.OpenInRoot(dir, name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

func redirectToDir(w http.ResponseWriter, r *http.Request, name string) {
	target := "/" + name + "/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}
