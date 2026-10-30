package proxy

import (
	"net/http"
	"net/url"
	"strings"
)

// redirectHandler answers every request with a permanent redirect to the
// same path and query under the configured base URL. GET and HEAD get 301;
// other methods get 308 so a form or API call keeps its method and body.
// The base's scheme and host are fixed by the operator: a request path such
// as "//evil.example" stays a path under that host.
type redirectHandler struct{ base *url.URL }

func (h redirectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	to := *h.base
	to.Path = strings.TrimSuffix(h.base.Path, "/") + r.URL.Path
	to.RawPath = ""
	if r.URL.RawPath != "" {
		to.RawPath = strings.TrimSuffix(h.base.EscapedPath(), "/") + r.URL.RawPath
	}
	to.RawQuery = r.URL.RawQuery
	code := http.StatusMovedPermanently
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		code = http.StatusPermanentRedirect
	}
	w.Header().Set("Location", to.String())
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(code)
}
