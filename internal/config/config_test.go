package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":8888" {
		t.Errorf("Listen = %q, want :8888", cfg.Listen)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("len(Routes) = %d, want 1", len(cfg.Routes))
	}
}

func TestLoadRejectsMissingListen(t *testing.T) {
	path := writeConfig(t, `{"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for missing listen address")
	}
}

func TestLoadRejectsNoRoutes(t *testing.T) {
	path := writeConfig(t, `{"listen": ":8888", "routes": []}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for empty routes")
	}
}

func TestLoadRejectsRouteMissingTarget(t *testing.T) {
	path := writeConfig(t, `{"listen": ":8888", "routes": [{"host": "a.localhost"}]}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for a route missing target")
	}
}

func TestLoadRejectsTLSWithoutDomains(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"tls": {"enabled": true, "cache_dir": "/tmp/cache"},
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for tls.enabled without domains")
	}
}

func TestLoadRejectsTLSWithoutCacheDir(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"tls": {"enabled": true, "domains": ["example.com"]},
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for tls.enabled without cache_dir")
	}
}

func TestLoadRejectsAuthRequiredWithoutAuthConfig(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000", "auth_required": true}]
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for auth_required without an auth config")
	}
}

func TestLoadAcceptsAuthRequiredWithFullAuthConfig(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"auth": {
			"issuer": "https://accounts.google.com",
			"client_id": "id",
			"client_secret": "secret",
			"redirect_url": "http://localhost:8888/_/auth/callback"
		},
		"routes": [{"host": "localhost:8888", "target": "http://localhost:9000", "auth_required": true}]
	}`)
	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadRejectsAuthRequiredOnDifferentHostWithoutCookieDomain(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"auth": {
			"issuer": "https://accounts.google.com",
			"client_id": "id",
			"client_secret": "secret",
			"redirect_url": "http://auth.example.com/_/auth/callback"
		},
		"routes": [{"host": "app.example.com", "target": "http://localhost:9000", "auth_required": true}]
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error: auth_required route on a different host than redirect_url, with no cookie_domain to share the session")
	}
}

func TestLoadAcceptsAuthRequiredOnDifferentHostWithCookieDomain(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"auth": {
			"issuer": "https://accounts.google.com",
			"client_id": "id",
			"client_secret": "secret",
			"redirect_url": "http://auth.example.com/_/auth/callback",
			"cookie_domain": ".example.com"
		},
		"routes": [{"host": "app.example.com", "target": "http://localhost:9000", "auth_required": true}]
	}`)
	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadRejectsMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected an error for a missing config file")
	}
}

func TestLoadAcceptsAccessPolicyWithFullAuthConfig(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"auth": {
			"issuer": "https://accounts.google.com",
			"client_id": "id",
			"client_secret": "secret",
			"redirect_url": "http://localhost:8888/_/auth/callback"
		},
		"routes": [{
			"host": "localhost:8888",
			"target": "http://localhost:9000",
			"access": {
				"allowed_domains": ["example.com"],
				"required_groups": ["engineering"]
			}
		}]
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Routes[0].Access == nil {
		t.Fatal("Access = nil, want decoded policy")
	}
	if got := cfg.Routes[0].Access.AllowedDomains; len(got) != 1 || got[0] != "example.com" {
		t.Fatalf("AllowedDomains = %v, want [example.com]", got)
	}
}

func TestLoadRejectsAccessPolicyWithoutAuthConfig(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000", "access": {}}]
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for access policy without an auth config")
	}
}

func TestLoadNormalizesRouteHosts(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"routes": [{"host": "Magiclink.Localhost.:8888", "target": "http://localhost:9000"}]
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Routes[0].Host; got != "magiclink.localhost:8888" {
		t.Fatalf("Host = %q, want magiclink.localhost:8888", got)
	}
}

func TestLoadRejectsDuplicateNormalizedHosts(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"routes": [
			{"host": "APP.localhost", "target": "http://localhost:9000"},
			{"host": "app.localhost.", "target": "http://localhost:9001"}
		]
	}`)

	if _, err := Load(path); err == nil {
		t.Fatal("expected duplicate normalized route hosts to be rejected")
	}
}

func TestLoadRejectsUnsupportedTargetScheme(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"routes": [{"host": "app.localhost", "target": "ftp://localhost:9000"}]
	}`)

	if _, err := Load(path); err == nil {
		t.Fatal("expected an unsupported target scheme to be rejected")
	}
}

func TestLoadNormalizesAndValidatesCookieDomain(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"auth": {
			"issuer": "https://accounts.google.com",
			"client_id": "id",
			"client_secret": "secret",
			"redirect_url": "https://AUTH.example.com/_/auth/callback",
			"cookie_domain": ".Example.COM"
		},
		"routes": [{
			"host": "APP.example.com",
			"target": "http://localhost:9000",
			"access": {}
		}]
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.CookieDomain != "example.com" {
		t.Fatalf("CookieDomain = %q, want example.com", cfg.Auth.CookieDomain)
	}
}

func TestLoadRejectsCookieDomainThatDoesNotCoverCallback(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"auth": {
			"issuer": "https://accounts.google.com",
			"client_id": "id",
			"client_secret": "secret",
			"redirect_url": "https://auth.example.com/_/auth/callback",
			"cookie_domain": ".other.example"
		},
		"routes": [{"host": "auth.example.com", "target": "http://localhost:9000", "access": {}}]
	}`)

	if _, err := Load(path); err == nil {
		t.Fatal("expected cookie_domain not covering redirect host to be rejected")
	}
}

func TestLoadRejectsCookieDomainThatDoesNotCoverProtectedRoute(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"auth": {
			"issuer": "https://accounts.google.com",
			"client_id": "id",
			"client_secret": "secret",
			"redirect_url": "https://auth.example.com/_/auth/callback",
			"cookie_domain": ".auth.example.com"
		},
		"routes": [{"host": "app.example.com", "target": "http://localhost:9000", "access": {}}]
	}`)

	if _, err := Load(path); err == nil {
		t.Fatal("expected cookie_domain not covering protected route to be rejected")
	}
}

func TestLoadRejectsNonHTTPRedirectURL(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"auth": {
			"issuer": "https://accounts.google.com",
			"client_id": "id",
			"client_secret": "secret",
			"redirect_url": "ftp://app.example.com/_/auth/callback"
		},
		"routes": [{"host": "app.example.com", "target": "http://localhost:9000", "access": {}}]
	}`)

	if _, err := Load(path); err == nil {
		t.Fatal("expected non-HTTP auth.redirect_url to be rejected")
	}
}

func TestLoadDefaultsAdminListener(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Admin.Listen != DefaultAdminListen {
		t.Fatalf("Admin.Listen = %q, want %q", cfg.Admin.Listen, DefaultAdminListen)
	}
}

func TestParseRejectsUnknownAndMultipleValues(t *testing.T) {
	valid := `{"listen":":8888","routes":[{"host":"a.localhost","target":"http://localhost:9000"}]}`
	if _, err := Parse([]byte(`{"listen":":8888","typo":true,"routes":[{"host":"a.localhost","target":"http://localhost:9000"}]}`)); err == nil {
		t.Fatal("unknown field returned nil error")
	}
	if _, err := Parse([]byte(valid + valid)); err == nil {
		t.Fatal("multiple JSON values returned nil error")
	}
}

func TestValidateDoesNotMutateInput(t *testing.T) {
	cfg := Config{
		Listen: ":8888",
		Routes: []Route{{
			Host:   "APP.Localhost.",
			Target: "http://localhost:9000",
			Access: &AccessPolicy{AllowedEmails: []string{"user@example.com"}},
		}},
		Auth: Auth{
			Issuer:       "https://issuer.example",
			ClientID:     "id",
			ClientSecret: "secret",
			RedirectURL:  "http://app.localhost/_/auth/callback",
		},
	}
	normalized, err := Validate(cfg)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	normalized.Routes[0].Access.AllowedEmails[0] = "changed@example.com"
	if cfg.Routes[0].Host != "APP.Localhost." || cfg.Routes[0].Access.AllowedEmails[0] != "user@example.com" {
		t.Fatalf("Validate mutated input: %+v", cfg.Routes[0])
	}
}

func TestWriteAtomicRoundTripsAndPreservesPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	cfg := Config{
		Listen: ":8888",
		Admin:  Admin{Token: "secret"},
		Routes: []Route{{Host: "APP.Localhost.", Target: "http://localhost:9000"}},
	}
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Routes[0].Host != "app.localhost" || loaded.Admin.Token != "secret" {
		t.Fatalf("persisted config = %+v", loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); hasUnixPermissions() && got != 0o640 {
		t.Fatalf("mode = %o, want 640", got)
	}
}

func TestValidateRejectsNonLoopbackAdminListener(t *testing.T) {
	cfg := Config{
		Listen: ":8888",
		Admin:  Admin{Listen: "0.0.0.0:9090"},
		Routes: []Route{{Host: "app.localhost", Target: "http://localhost:9000"}},
	}
	if _, err := Validate(cfg); err == nil {
		t.Fatal("non-loopback admin listener returned nil error")
	}

	cfg.Admin.Listen = "[::1]:9090"
	if _, err := Validate(cfg); err != nil {
		t.Fatalf("IPv6 loopback listener rejected: %v", err)
	}
}

func TestWriteAtomicKeepsSymlinkAndUpdatesTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.json")
	if err := os.WriteFile(target, []byte("{\"listen\":\":9999\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Listen: ":8888", Routes: []Route{{
		Host:   "app.localhost",
		Target: "http://localhost:9000",
	}}}
	if err := WriteAtomic(link, cfg); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("WriteAtomic replaced the symlink with a regular file")
	}
	loaded, err := Load(link)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Routes) != 1 || loaded.Routes[0].Host != "app.localhost" {
		t.Fatalf("target config = %+v, want the written routes", loaded.Routes)
	}
}

func TestValidateHealthPathMustBeAbsolute(t *testing.T) {
	base := func(healthPath string) Config {
		return Config{Listen: ":8888", Routes: []Route{{
			Host:       "app.localhost",
			Target:     "http://localhost:9000",
			HealthPath: healthPath,
		}}}
	}
	if _, err := Validate(base("healthz")); err == nil {
		t.Fatal("Validate accepted a relative health_path")
	}
	if _, err := Validate(base("/healthz")); err != nil {
		t.Fatalf("Validate(absolute health_path): %v", err)
	}
}

func TestValidateStaticRoutes(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	route := func(r Route) Config {
		r.Host = "site.localhost"
		return Config{Listen: ":8888", Routes: []Route{r}}
	}

	got, err := Validate(route(Route{Root: "/srv/sites/blog/", SPA: true}))
	if err != nil {
		t.Fatalf("Validate(static route): %v", err)
	}
	if got.Routes[0].Root != "/srv/sites/blog" || !got.Routes[0].IsStatic() {
		t.Fatalf("Root = %q, IsStatic = %v; want cleaned /srv/sites/blog and true", got.Routes[0].Root, got.Routes[0].IsStatic())
	}

	rejected := map[string]Route{
		"neither target nor root": {},
		"both target and root":    {Target: "http://localhost:9000", Root: "/srv/site"},
		"relative root":           {Root: "sites/blog"},
		"root with health_path":   {Root: "/srv/site", HealthPath: "/health"},
		"spa without root":        {Target: "http://localhost:9000", SPA: true},
	}
	for name, r := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := Validate(route(r)); err == nil {
				t.Fatalf("Validate accepted %+v", r)
			}
		})
	}
}

func TestUpstreamTimeoutDefaultsAndRejectsOutOfRange(t *testing.T) {
	doc := func(extra string) []byte {
		return []byte(`{"listen": ":8888", ` + extra + `"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]}`)
	}
	cfg, err := Parse(doc(""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.UpstreamTimeout(); got != DefaultUpstreamTimeout {
		t.Fatalf("default = %v, want %v", got, DefaultUpstreamTimeout)
	}
	cfg, err = Parse(doc(`"upstream_timeout_seconds": 5, `))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.UpstreamTimeout(); got != 5*time.Second {
		t.Fatalf("configured = %v, want 5s", got)
	}
	for _, bad := range []string{"-1", "3601"} {
		if _, err := Parse(doc(`"upstream_timeout_seconds": ` + bad + `, `)); err == nil {
			t.Errorf("upstream_timeout_seconds %s was accepted", bad)
		}
	}
}

func TestOriginClientCARequiresTLS(t *testing.T) {
	const routes = `"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]`
	if _, err := Parse([]byte(`{"listen": ":8888", "tls": {"origin_client_ca_file": "/etc/switchboard/origin-pull-ca.pem"}, ` + routes + `}`)); err == nil {
		t.Fatal("tls.origin_client_ca_file without tls.enabled was accepted: it would be silently ignored")
	}
	cfg, err := Parse([]byte(`{"listen": ":8888", "tls": {"enabled": true, "domains": ["example.com"], "cache_dir": "/tmp/c", "origin_client_ca_file": "/etc/switchboard/origin-pull-ca.pem"}, ` + routes + `}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.TLS.OriginClientCAFile != "/etc/switchboard/origin-pull-ca.pem" {
		t.Fatalf("OriginClientCAFile = %q", cfg.TLS.OriginClientCAFile)
	}
}

func TestValidateSiteRoutes(t *testing.T) {
	pagesDir := t.TempDir() // absolute on every OS
	withPages := func(r Route) Config {
		r.Host = "blog.localhost"
		return Config{Listen: ":8888", Pages: &Pages{Dir: pagesDir}, Routes: []Route{r}}
	}

	got, err := Validate(withPages(Route{Site: "blog", SPA: true}))
	if err != nil {
		t.Fatalf("Validate(site route): %v", err)
	}
	if r := got.Routes[0]; !r.IsStatic() || r.Destination() != "site:blog" {
		t.Fatalf("site route: IsStatic = %v, Destination = %q", r.IsStatic(), r.Destination())
	}

	rejected := map[string]Config{
		"site without pages":     {Listen: ":8888", Routes: []Route{{Host: "blog.localhost", Site: "blog"}}},
		"site and root":          withPages(Route{Site: "blog", Root: pagesDir}),
		"site and target":        withPages(Route{Site: "blog", Target: "http://localhost:9000"}),
		"site name with a slash": withPages(Route{Site: "../etc"}),
		"uppercase site name":    withPages(Route{Site: "Blog"}),
		"site with health_path":  withPages(Route{Site: "blog", HealthPath: "/health"}),
		"relative pages dir":     {Listen: ":8888", Pages: &Pages{Dir: "pages"}, Routes: []Route{{Host: "blog.localhost", Site: "blog"}}},
		"negative keep":          {Listen: ":8888", Pages: &Pages{Dir: pagesDir, Keep: -1}, Routes: []Route{{Host: "blog.localhost", Site: "blog"}}},
	}
	for name, cfg := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := Validate(cfg); err == nil {
				t.Fatalf("Validate accepted %+v", cfg)
			}
		})
	}
}

func TestValidateAdminSocket(t *testing.T) {
	for path, ok := range map[string]bool{
		"":                             true,
		"/run/switchboard/admin.sock":  true,
		"run/admin.sock":               false,
		"/run/switchboard/../x.sock":   false,
		"/" + strings.Repeat("a", 107): false,
	} {
		cfg := Config{
			Listen: ":8888",
			Admin:  Admin{Socket: path},
			Routes: []Route{{Host: "app.localhost", Target: "http://localhost:9000"}},
		}
		if _, err := Validate(cfg); (err == nil) != ok {
			t.Errorf("admin.socket %q: err = %v, want ok=%v", path, err, ok)
		}
	}
}
