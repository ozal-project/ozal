package pages

import (
	"errors"
	"strings"
	"testing"
)

func mustRules(t *testing.T, redirects, headers string) *Rules {
	t.Helper()
	r, err := ParseRules([]byte(redirects), []byte(headers))
	if err != nil {
		t.Fatalf("ParseRules: %v", err)
	}
	return r
}

func TestRedirects(t *testing.T) {
	r := mustRules(t, `
# moved pages
/old            /new
/temp           /elsewhere          302
/blog/*         /posts/:splat
/docs*          /manual/:splat      308
/u/:name        https://example.com/people/:name  307
/a/:x/b/:y      /c/:y/:x
/               /home               302
/first          /one
/first          /two
`, "")
	for _, tc := range []struct {
		path   string
		to     string
		status int
	}{
		{"/old", "/new", 301},
		{"/temp", "/elsewhere", 302},
		{"/blog/2026/hello", "/posts/2026/hello", 301},
		{"/blog/", "/posts/", 301},
		{"/docs", "/manual/", 308},
		{"/docs/guide", "/manual//guide", 308}, // literal splat; harmless, still one leading slash
		{"/u/ana", "https://example.com/people/ana", 307},
		{"/a/1/b/2", "/c/2/1", 301},
		{"/", "/home", 302},
		{"/first", "/one", 301}, // first matching rule wins
	} {
		to, status, ok := r.Redirect(tc.path)
		if !ok || to != tc.to || status != tc.status {
			t.Errorf("Redirect(%q) = %q %d %v, want %q %d", tc.path, to, status, ok, tc.to, tc.status)
		}
	}
	for _, path := range []string{"/oldx", "/old/", "/blog", "/u/", "/u/ana/x", "/a/1/b", "/nothing"} {
		if to, _, ok := r.Redirect(path); ok {
			t.Errorf("Redirect(%q) = %q, want no match", path, to)
		}
	}
}

// The visitor controls the splat. "/go//evil.example" must not become the
// protocol-relative "//evil.example", which browsers follow off-site.
func TestRedirectNeverBecomesProtocolRelative(t *testing.T) {
	r := mustRules(t, "/go/* /:splat\n/x/:n /:n\n", "")
	for _, path := range []string{"/go//evil.example", "/go/\\evil.example", "/go/a\r\nSet-Cookie: x=1"} {
		if to, _, ok := r.Redirect(path); ok {
			t.Errorf("Redirect(%q) = %q, want refused", path, to)
		}
	}
	if to, _, ok := r.Redirect("/go/fine/page"); !ok || to != "/fine/page" {
		t.Fatalf("ordinary splat = %q %v", to, ok)
	}
}

func TestHeaders(t *testing.T) {
	r := mustRules(t, "", `
# every page
/*
  X-Frame-Options: DENY
  Cache-Control: no-cache
/assets/*
  Cache-Control: public, max-age=31536000, immutable
/embed/*
  ! X-Frame-Options
/p/:id
  x-robots-tag: noindex
`)
	got := func(path string) map[string]string {
		out := map[string]string{}
		for _, h := range r.Headers(path) {
			out[h.Name] = h.Value
		}
		return out
	}
	if h := got("/"); h["X-Frame-Options"] != "DENY" || h["Cache-Control"] != "no-cache" {
		t.Errorf("/ headers = %v", h)
	}
	if h := got("/assets/app.js"); h["Cache-Control"] != "no-cache, public, max-age=31536000, immutable" {
		t.Errorf("repeated header must be joined, got %v", h)
	}
	if h := got("/embed/x"); h["X-Frame-Options"] != "" || h["Cache-Control"] != "no-cache" {
		t.Errorf("detach must remove only that header, got %v", h)
	}
	if h := got("/p/7"); h["X-Robots-Tag"] != "noindex" {
		t.Errorf("names are canonicalised, got %v", h)
	}
	var nilRules *Rules
	if nilRules.Headers("/") != nil {
		t.Error("nil rules have no headers")
	}
	if _, _, ok := nilRules.Redirect("/"); ok {
		t.Error("nil rules have no redirects")
	}
}

func TestRulesRefused(t *testing.T) {
	for _, tc := range []struct {
		name, redirects, headers string
		line                     int
	}{
		{"rewrite status", "/a /b 200", "", 1},
		{"unknown status", "/a /b 418", "", 1},
		{"too many fields", "/a /b 301 extra", "", 1},
		{"one field", "\n/a", "", 2},
		{"absolute pattern", "https://x.example/a /b", "", 1},
		{"protocol-relative destination", "/a //evil.example", "", 1},
		{"javascript destination", "/a javascript:alert(1)", "", 1},
		{"splat in the middle", "/a/*/b /c", "", 1},
		{"undefined placeholder", "/a/:x /b/:y", "", 1},
		{"splat without star", "/a /b/:splat", "", 1},
		{"placeholder in host", "/a/:h https://:h.example/", "", 1},
		{"splat in host", "/a/* https://x.example:splat", "", 1},
		{"credentials in URL", "/a https://user@x.example/", "", 1},
		{"backslash", `/a /b\c`, "", 1},
		{"repeated placeholder", "/:a/:a /b", "", 1},
		{"header before pattern", "", "  X-A: b", 1},
		{"set-cookie", "", "/*\n  Set-Cookie: s=1; Domain=.example.com", 2},
		{"content-length", "", "/*\n  content-length: 0", 2},
		{"nosniff override", "", "/*\n  X-Content-Type-Options: none", 2},
		{"bad header name", "", "/*\n  Bad Name: x", 2},
		{"empty value", "", "/*\n  X-A:", 2},
		{"pattern without headers", "", "/a\n/b\n  X-A: 1", 1},
		{"trailing pattern", "", "/a\n  X-A: 1\n/b", 3},
		{"long line", "/" + strings.Repeat("a", 1001) + " /b", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRules([]byte(tc.redirects), []byte(tc.headers))
			var re *RulesError
			if !errors.As(err, &re) {
				t.Fatalf("err = %v, want a RulesError", err)
			}
			if re.Line != tc.line {
				t.Errorf("line = %d, want %d (%v)", re.Line, tc.line, err)
			}
		})
	}
}

func TestRulesLimits(t *testing.T) {
	var b strings.Builder
	for i := range maxDynamicRedirects + 1 {
		b.WriteString("/d" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + "/* /x\n")
	}
	if _, err := ParseRules([]byte(b.String()), nil); err == nil {
		t.Fatal("more dynamic redirects than the limit must be refused")
	}
	if _, err := ParseRules(make([]byte, MaxRulesFileBytes+1), nil); err == nil {
		t.Fatal("an oversized file must be refused")
	}
}

// FuzzRules: parsing never panics, and a redirect to a site path never
// leaves the site, whatever the rules and the request path.
func FuzzRules(f *testing.F) {
	f.Add("/go/* /:splat", "/*\n  X-A: b", "/go//evil.example")
	f.Add("/u/:n https://x.example/:n 302", "/p/:id\n  ! X-A", "/u/a")
	f.Add("/docs* /m/:splat", "", "/docs/\\x")
	f.Fuzz(func(t *testing.T, redirects, headers, path string) {
		r, err := ParseRules([]byte(redirects), []byte(headers))
		if err != nil {
			return
		}
		r.Headers(path)
		to, _, ok := r.Redirect(path)
		if !ok {
			return
		}
		if strings.ContainsFunc(to, isControl) {
			t.Fatalf("redirect %q has a control character", to)
		}
		if !strings.HasPrefix(to, "http://") && !strings.HasPrefix(to, "https://") &&
			(!strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") || strings.HasPrefix(to, "/\\")) {
			t.Fatalf("rules %q sent %q off-site to %q", redirects, path, to)
		}
	})
}
