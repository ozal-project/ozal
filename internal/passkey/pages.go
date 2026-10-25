package passkey

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

//go:embed assets
var assets embed.FS

// pageHeaders are set on every login page and asset: no inline or foreign
// script can run, nothing can frame the page (clickjacking a passkey
// prompt), and the enrolment URL never leaks in a Referer.
func pageHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; connect-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cache-Control", "no-store")
}

// assetVersions maps each script and stylesheet to a short hash of its
// content. Pages link them as name?v=hash, so a browser or Cloudflare can
// keep a copy for good: a new binary with a changed file links a new URL.
// Every round trip to the origin costs ~300 ms from far away (2026-10-21).
var assetVersions = sync.OnceValue(func() map[string]string {
	versions := map[string]string{}
	for _, name := range []string{"passkey.js", "passkey.css"} {
		body, err := assets.ReadFile("assets/" + name)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(body)
		versions[name] = hex.EncodeToString(sum[:6])
	}
	return versions
})

// versioned rewrites a page's asset links to their content-hashed URLs.
func versioned(page []byte) []byte {
	for name, v := range assetVersions() {
		page = bytes.ReplaceAll(page, []byte(`"/_/auth/`+name+`"`), []byte(`"/_/auth/`+name+`?v=`+v+`"`))
	}
	return page
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
	case portalPath:
		name = "assets/me.html"
	}
	body, err := assets.ReadFile(name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	body = versioned(body)
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
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".woff2": "font/woff2",
	".svg":   "image/svg+xml",
}

func serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := path.Base(r.URL.Path)
	dir := "assets/"
	if path.Dir(r.URL.Path) == "/_/auth/icons" {
		dir = "assets/icons/"
	}
	if path.Ext(name) != ".svg" && dir == "assets/icons/" {
		http.NotFound(w, r)
		return
	}
	body, err := assets.ReadFile(dir + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pageHeaders(w.Header())
	// Only the exact current version is cacheable forever; any other URL is
	// served fresh, so an old link never pins old code.
	if v := r.URL.Query().Get("v"); v != "" && v == assetVersions()[name] {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else if strings.HasSuffix(name, ".woff2") || dir == "assets/icons/" {
		// The font and the provider icons are linked without a version;
		// they change only with a new release of their source.
		w.Header().Set("Cache-Control", "public, max-age=604800")
	}
	// Fixed per extension, never the OS mime table (see the console).
	w.Header().Set("Content-Type", assetTypes[path.Ext(name)])
	_, _ = w.Write(body)
}
