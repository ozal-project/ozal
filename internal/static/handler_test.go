package static

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/pages"
)

// newSite builds a site directory next to a secret file it must never serve.
func newSite(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	site := filepath.Join(base, "site")
	files := map[string]string{
		"site/index.html":               "home",
		"site/about.html":               "about",
		"site/docs/index.html":          "docs",
		"site/assets/app.js":            "js",
		"site/.env":                     "SECRET=site",
		"site/_headers":                 "/*\n  X-Frame-Options: DENY",
		"site/_redirects":               "/old /new 301",
		"site/.well-known/security.txt": "contact",
		"secret.txt":                    "outside the site",
	}
	for name, body := range files {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(base, "secret.txt"), filepath.Join(site, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base, filepath.Join(site, "up")); err != nil {
		t.Fatal(err)
	}
	return site
}

func serve(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandlerServesFiles(t *testing.T) {
	h := Handler{Dir: newSite(t)}
	tests := []struct {
		target string
		status int
		body   string
	}{
		{"/", http.StatusOK, "home"},
		{"/about.html", http.StatusOK, "about"},
		{"/docs/", http.StatusOK, "docs"},
		{"/assets/app.js", http.StatusOK, "js"},
		{"/.well-known/security.txt", http.StatusOK, "contact"},
		{"/missing", http.StatusNotFound, ""},
		{"/assets/", http.StatusNotFound, ""}, // no index.html and never a listing
	}
	for _, tt := range tests {
		rec := serve(h, http.MethodGet, tt.target)
		if rec.Code != tt.status {
			t.Errorf("GET %s = %d, want %d", tt.target, rec.Code, tt.status)
			continue
		}
		if tt.body != "" && rec.Body.String() != tt.body {
			t.Errorf("GET %s body = %q, want %q", tt.target, rec.Body.String(), tt.body)
		}
	}
	if got := serve(h, http.MethodGet, "/assets/app.js").Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// TestHandlerNeverLeavesTheSite is the regression test for PAGES-001: none of
// these requests may return the secret outside the site or a hidden file.
func TestHandlerNeverLeavesTheSite(t *testing.T) {
	h := Handler{Dir: newSite(t), SPA: true}
	attacks := []string{
		"/../secret.txt",
		"/%2e%2e/secret.txt",
		"/docs/%2e%2e/%2e%2e/secret.txt",
		"/..%2fsecret.txt",
		"/..%5csecret.txt",
		"/escape.txt",
		"/up/secret.txt",
		"/.env",
		"/%2eenv",
		"/index.html%00",
	}
	for _, target := range attacks {
		rec := serve(h, http.MethodGet, target)
		body := rec.Body.String()
		if strings.Contains(body, "outside the site") || strings.Contains(body, "SECRET") {
			t.Errorf("GET %s leaked %q (status %d)", target, body, rec.Code)
		}
	}
	for _, target := range []string{"/escape.txt", "/up/secret.txt", "/.env"} {
		if rec := serve(h, http.MethodGet, target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}
	}
}

func TestHandlerRedirectsDirectoriesToTrailingSlash(t *testing.T) {
	h := Handler{Dir: newSite(t)}
	tests := []struct{ target, location string }{
		{"/docs?x=1", "/docs/?x=1"},
		{"//docs", "/docs/"}, // never "//docs/", which browsers treat as a host
	}
	for _, tt := range tests {
		rec := serve(h, http.MethodGet, tt.target)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != tt.location {
			t.Errorf("GET %s = %d Location %q, want 301 %q", tt.target, rec.Code, rec.Header().Get("Location"), tt.location)
		}
	}
}

func TestHandlerSPAFallback(t *testing.T) {
	site := newSite(t)
	spa := Handler{Dir: site, SPA: true}
	if rec := serve(spa, http.MethodGet, "/dashboard/settings"); rec.Code != http.StatusOK || rec.Body.String() != "home" {
		t.Errorf("SPA route = %d %q, want 200 home", rec.Code, rec.Body.String())
	}
	if rec := serve(spa, http.MethodGet, "/assets/missing.js"); rec.Code != http.StatusNotFound {
		t.Errorf("missing asset with SPA = %d, want 404", rec.Code)
	}
	if rec := serve(Handler{Dir: site}, http.MethodGet, "/dashboard/settings"); rec.Code != http.StatusNotFound {
		t.Errorf("without SPA = %d, want 404", rec.Code)
	}
}

func TestHandlerOnlyAllowsReads(t *testing.T) {
	h := Handler{Dir: newSite(t)}
	if rec := serve(h, http.MethodHead, "/"); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD / = %d with %d bytes, want 200 and no body", rec.Code, rec.Body.Len())
	}
	rec := serve(h, http.MethodPost, "/")
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST / = %d Allow %q, want 405 GET, HEAD", rec.Code, rec.Header().Get("Allow"))
	}
}

// Pages reads _headers and _redirects to configure the host; it does not
// publish them. Serving them would hand out the site's routing and caching
// rules, and a SPA fallback must not turn them into the home page either.
func TestHandlerDoesNotPublishPagesControlFiles(t *testing.T) {
	for _, spa := range []bool{false, true} {
		h := Handler{Dir: newSite(t), SPA: spa}
		for _, target := range []string{"/_headers", "/_redirects", "/./_headers", "/a/../_redirects"} {
			rec := serve(h, http.MethodGet, target)
			if rec.Code != http.StatusNotFound {
				t.Errorf("spa=%v GET %s = %d, want 404", spa, target, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "X-Frame-Options") || strings.Contains(rec.Body.String(), "/old") {
				t.Errorf("spa=%v GET %s leaked the control file: %q", spa, target, rec.Body.String())
			}
		}
	}
}

func TestHandlerFollowsTheLiveDeployment(t *testing.T) {
	v1, v2 := t.TempDir(), t.TempDir()
	for dir, body := range map[string]string{v1: "one", v2: "two"} {
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	current, deployed := "", false
	h := Handler{Live: func() (pages.Live, bool) { return pages.Live{Dir: current}, deployed }}
	get := func() (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec.Code, rec.Body.String()
	}

	if code, _ := get(); code != http.StatusServiceUnavailable {
		t.Fatalf("no deployment yet: status %d, want 503", code)
	}
	current, deployed = v1, true
	if code, body := get(); code != http.StatusOK || body != "one" {
		t.Fatalf("live v1: %d %q", code, body)
	}
	current = v2 // a deploy or rollback: the next request sees it
	if code, body := get(); code != http.StatusOK || body != "two" {
		t.Fatalf("live v2: %d %q", code, body)
	}
}

func TestHandlerAppliesTheDeploymentRules(t *testing.T) {
	dir := newSite(t)
	rules, err := pages.ParseRules(
		[]byte("/about.html /about-us 301\n/go/* /:splat 302\n"),
		[]byte("/*\n  X-Frame-Options: DENY\n/docs/*\n  Cache-Control: no-store\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Live: func() (pages.Live, bool) { return pages.Live{Dir: dir, Rules: rules}, true }}
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	// The file exists, and the redirect still wins (Cloudflare Pages does the same).
	if rec := get("/about.html"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/about-us" {
		t.Fatalf("/about.html = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec := get("/docs/")
	if rec.Code != http.StatusOK || rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("/docs/ = %d %v", rec.Code, rec.Header())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("rules must not drop the nosniff baseline")
	}
	// A raw "//" in the visitor's path must not turn into an off-site redirect.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = "/go//evil.example"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); strings.HasPrefix(loc, "//") {
		t.Fatalf("open redirect to %q", loc)
	}
}
