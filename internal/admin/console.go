package admin

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// consoleApp is the Svelte console built by scripts/build-console.sh from
// web/console. It is committed so `go build` alone produces a complete binary;
// Node is a build-time tool only.
//
//go:embed all:console
var consoleApp embed.FS

// consoleCSP keeps the app to same-origin assets and API calls. Vite emits no
// inline scripts, so 'unsafe-inline' is never needed.
const consoleCSP = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'"

// consoleTypes fixes the Content-Type of every file kind Vite emits. Without
// it ServeContent asks the OS mime table, and on Windows the registry may
// answer something other than text/javascript, which nosniff then refuses.
var consoleTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
	".ttf":   "font/ttf",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

// consoleRoutes are the client-side pages; each one is served index.html.
var consoleRoutes = map[string]bool{"/": true, "/activity": true, "/routes": true, "/people": true, "/sites": true, "/simulator": true}

// console serves the embedded app: a real file when one exists, index.html
// for a known client route, 404 otherwise. Control-plane paths under /_/ never
// fall through to the app.
func (h *Handler) console(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/_/") {
		http.NotFound(w, r)
		return
	}
	dist, err := fs.Sub(consoleApp, "console")
	if err != nil {
		http.Error(w, "console unavailable", http.StatusInternalServerError)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if consoleRoutes[r.URL.Path] || name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(dist, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hdr := w.Header()
	if ctype, ok := consoleTypes[path.Ext(name)]; ok {
		hdr.Set("Content-Type", ctype)
	}
	hdr.Set("Content-Security-Policy", consoleCSP)
	hdr.Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(name, "assets/") {
		// Vite puts a content hash in every asset name.
		hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		hdr.Set("Cache-Control", "no-store")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// apiStatus is the JSON form of the route debug view for the console's routes
// page. It is the same projection as /_/status: no secrets, no allowlists.
func (h *Handler) apiStatus(w http.ResponseWriter, _ *http.Request) {
	view := h.buildStatusView()
	writeJSON(w, http.StatusOK, struct {
		GeneratedAt  string             `json:"generated_at"`
		ConfigSHA256 string             `json:"config_sha256"`
		Certificate  *CertificateStatus `json:"certificate,omitempty"`
		Audit        *AuditStatus       `json:"audit,omitempty"`
		Routes       []routeView        `json:"routes"`
	}{view.GeneratedAt, h.manager.ConfigDigest(), view.Certificate, view.Audit, view.Routes})
}
