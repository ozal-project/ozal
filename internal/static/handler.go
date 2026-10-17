package static

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

const indexFile = "index.html"

// Handler serves files from Dir. It never lists directories: a directory is
// served through its index.html. With SPA set, a missing extensionless path
// falls back to the root index.html so client-side routers work.
type Handler struct {
	Dir string
	SPA bool
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

	file, info, err := h.open(name)
	if err == nil && info.IsDir() {
		file.Close()
		if !strings.HasSuffix(r.URL.Path, "/") {
			// Built from the cleaned name, never from r.URL.Path: a raw
			// "//evil.example" would become a protocol-relative open redirect.
			redirectToDir(w, r, name)
			return
		}
		file, info, err = h.open(path.Join(name, indexFile))
	}
	if errors.Is(err, fs.ErrNotExist) && h.SPA && path.Ext(name) == "" {
		file, info, err = h.open(indexFile)
	}
	if err != nil || info.IsDir() {
		if file != nil {
			file.Close()
		}
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

// open opens name inside Dir. os.OpenInRoot fails if name, or any symlink it
// follows, would leave Dir.
func (h Handler) open(name string) (*os.File, fs.FileInfo, error) {
	file, err := os.OpenInRoot(h.Dir, name)
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
