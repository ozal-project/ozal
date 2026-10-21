package passkey

import (
	"bytes"
	"embed"
	"html"
	"net/http"
	"path"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

//go:embed assets
var assets embed.FS

// pageHeaders are set on every login page and asset: no inline or foreign
// script can run, nothing can frame the page (clickjacking a passkey
// prompt), and the enrolment URL never leaks in a Referer.
func pageHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cache-Control", "no-store")
}

// servePage serves the login or enrolment page, only on the login host:
// the browser signs for that origin, so a page on any other host would
// fail at the server anyway. Elsewhere it redirects, keeping the query
// (and the browser keeps the fragment across the redirect).
func (g *Gate) servePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if host, err := config.NormalizeHost(r.Host); err != nil || host != g.loginHost {
		http.Redirect(w, r, g.origin+r.URL.RequestURI(), http.StatusFound)
		return
	}
	name := "assets/login.html"
	switch r.URL.Path {
	case "/_/auth/enrol":
		name = "assets/enrol.html"
	case "/_/auth/devices":
		name = "assets/devices.html"
	}
	body, err := assets.ReadFile(name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if g.oidc != nil {
		// The page shows the provider's button only when one is configured;
		// the name is operator text, escaped like any other.
		body = bytes.Replace(body, []byte(`data-oidc=""`), []byte(`data-oidc="`+html.EscapeString(g.oidcName)+`"`), 1)
	}
	pageHeaders(w.Header())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

var assetTypes = map[string]string{
	".js":  "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8",
}

func serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := path.Base(r.URL.Path)
	body, err := assets.ReadFile("assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pageHeaders(w.Header())
	// Fixed per extension, never the OS mime table (see the console).
	w.Header().Set("Content-Type", assetTypes[path.Ext(name)])
	_, _ = w.Write(body)
}
