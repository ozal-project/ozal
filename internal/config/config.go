// Package config loads Switchboard's routing table from a JSON file.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const DefaultAdminListen = "127.0.0.1:9090"

type Route struct {
	Host         string        `json:"host"`
	Target       string        `json:"target,omitempty"`
	Root         string        `json:"root,omitempty"`          // static site directory; alternative to Target
	SPA          bool          `json:"spa,omitempty"`           // static only: unknown paths fall back to index.html
	HealthPath   string        `json:"health_path,omitempty"`   // defaults to "/" if empty
	AuthRequired bool          `json:"auth_required,omitempty"` // legacy shorthand for access: {}
	Access       *AccessPolicy `json:"access,omitempty"`
}

// IsStatic reports whether the route serves files from Root instead of
// proxying to Target.
func (r Route) IsStatic() bool {
	return r.Root != ""
}

// Destination is the human-readable upstream: the target URL, or the site
// directory for static routes.
func (r Route) Destination() string {
	if r.IsStatic() {
		return r.Root
	}
	return r.Target
}

// RequiresAuth reports whether a route needs an authenticated identity.
// Access being present implies authentication even when its policy is empty.
func (r Route) RequiresAuth() bool {
	return r.AuthRequired || r.Access != nil
}

type TLS struct {
	Enabled  bool     `json:"enabled"`
	Domains  []string `json:"domains,omitempty"`
	CacheDir string   `json:"cache_dir,omitempty"`
	Email    string   `json:"email,omitempty"`
}

type Auth struct {
	Issuer            string `json:"issuer,omitempty"`
	ClientID          string `json:"client_id,omitempty"`
	ClientSecret      string `json:"client_secret,omitempty"`
	RedirectURL       string `json:"redirect_url,omitempty"`
	SessionTTLMinutes int    `json:"session_ttl_minutes,omitempty"` // defaults to 480 (8h) if zero

	// CookieDomain, if set, shares the session cookie across every
	// subdomain of it (e.g. ".example.com" covers magiclink.example.com
	// and auditanchor.example.com alike). Required if a protected route is
	// on a different host than redirect_url's —
	// otherwise the session set during the OIDC callback (which always
	// happens on redirect_url's host) won't be sent back on the other
	// hosts' requests, and they'll redirect to login in a loop.
	CookieDomain string `json:"cookie_domain,omitempty"`
}

type Admin struct {
	Listen string `json:"listen,omitempty"`
	Token  string `json:"token,omitempty"`
}

// Limit is a token bucket: Rate requests per second with bursts up to Burst.
type Limit struct {
	Rate  float64 `json:"rate"`
	Burst int     `json:"burst"`
}

// RateLimit throttles requests per client IP. General applies to every
// request; Auth applies in addition to paths under /_/auth/ and should be
// much stricter. A nil RateLimit disables limiting.
type RateLimit struct {
	General Limit `json:"general"`
	Auth    Limit `json:"auth"`
}

type Config struct {
	Listen string `json:"listen"`
	TLS    TLS    `json:"tls,omitempty"`
	Auth   Auth   `json:"auth,omitempty"`
	Admin  Admin  `json:"admin,omitempty"`
	// TrustedProxies opts specific TCP peer IPs or CIDRs into CF-Connecting-IP.
	// Empty means no forwarding peer is trusted.
	TrustedProxies []string `json:"trusted_proxies,omitempty"`
	// DevErrors renders local-development error pages (upstream address,
	// hint, route list on 404). Local-only: it reveals internal addresses.
	DevErrors bool       `json:"dev_errors,omitempty"`
	RateLimit *RateLimit `json:"rate_limit,omitempty"`
	Routes    []Route    `json:"routes"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	return Parse(data)
}

// Parse decodes and validates a configuration document. Unknown fields are
// rejected so a misspelled security or routing option cannot be ignored.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parse config: multiple JSON values")
	}
	return Validate(cfg)
}

// Validate returns a normalized copy of cfg or an error.
func Validate(cfg Config) (*Config, error) {
	cfg = Clone(cfg)
	if cfg.Listen == "" {
		return nil, fmt.Errorf("config: listen address is required")
	}
	proxies, err := ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	for i, prefix := range proxies {
		cfg.TrustedProxies[i] = prefix.String()
	}
	if cfg.RateLimit != nil {
		for name, l := range map[string]Limit{"general": cfg.RateLimit.General, "auth": cfg.RateLimit.Auth} {
			if l.Rate <= 0 || l.Burst < 1 {
				return nil, fmt.Errorf("config: rate_limit.%s needs rate > 0 and burst >= 1", name)
			}
		}
	}
	if cfg.Admin.Listen == "" {
		cfg.Admin.Listen = DefaultAdminListen
	}
	if err := validateAdminListen(cfg.Admin.Listen); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if len(cfg.Routes) == 0 {
		return nil, fmt.Errorf("config: at least one route is required")
	}
	seenHosts := make(map[string]struct{}, len(cfg.Routes))
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.Host == "" {
			return nil, fmt.Errorf("config: route missing host: %+v", *r)
		}
		if (r.Target == "") == (r.Root == "") {
			return nil, fmt.Errorf("config: route %q needs exactly one of target or root", r.Host)
		}
		host, err := NormalizeHost(r.Host)
		if err != nil {
			return nil, fmt.Errorf("config: invalid route host %q: %w", r.Host, err)
		}
		if _, exists := seenHosts[host]; exists {
			return nil, fmt.Errorf("config: duplicate route host %q", host)
		}
		seenHosts[host] = struct{}{}
		r.Host = host
		if r.IsStatic() {
			if !filepath.IsAbs(r.Root) {
				return nil, fmt.Errorf("config: route %q root %q must be an absolute path", r.Host, r.Root)
			}
			if r.HealthPath != "" {
				return nil, fmt.Errorf("config: route %q serves a static root and cannot set health_path", r.Host)
			}
			r.Root = filepath.Clean(r.Root)
			continue
		}
		if r.SPA {
			return nil, fmt.Errorf("config: route %q sets spa but has no root", r.Host)
		}
		if _, err := ParseTarget(r.Target); err != nil {
			return nil, fmt.Errorf("config: route %q has invalid target %q: %w", r.Host, r.Target, err)
		}
		if r.HealthPath != "" && !strings.HasPrefix(r.HealthPath, "/") {
			return nil, fmt.Errorf("config: route %q health_path %q must start with /", r.Host, r.HealthPath)
		}
	}
	if cfg.TLS.Enabled {
		if len(cfg.TLS.Domains) == 0 {
			return nil, fmt.Errorf("config: tls.enabled requires at least one domain")
		}
		if cfg.TLS.CacheDir == "" {
			return nil, fmt.Errorf("config: tls.enabled requires cache_dir")
		}
	}

	if err := ValidateSecretReference("admin.token", cfg.Admin.Token); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	anyProtectedRoute := false
	for _, r := range cfg.Routes {
		if r.RequiresAuth() {
			anyProtectedRoute = true
			break
		}
	}
	if anyProtectedRoute {
		missing := []string{}
		if cfg.Auth.Issuer == "" {
			missing = append(missing, "issuer")
		}
		if cfg.Auth.ClientID == "" {
			missing = append(missing, "client_id")
		}
		if cfg.Auth.ClientSecret == "" {
			missing = append(missing, "client_secret")
		}
		if cfg.Auth.RedirectURL == "" {
			missing = append(missing, "redirect_url")
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("config: a route requires authentication but auth.%s is missing", missing[0])
		}
		if err := ValidateSecretReference("auth.client_secret", cfg.Auth.ClientSecret); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}

		redirectHost, err := hostOf(cfg.Auth.RedirectURL)
		if err != nil {
			return nil, fmt.Errorf("config: auth.redirect_url is not a valid URL: %w", err)
		}
		if cfg.Auth.CookieDomain != "" {
			cookieDomain, err := normalizeCookieDomain(cfg.Auth.CookieDomain)
			if err != nil {
				return nil, fmt.Errorf("config: %w", err)
			}
			if !cookieDomainCovers(redirectHost, cookieDomain) {
				return nil, fmt.Errorf(
					"config: auth.cookie_domain %q does not cover auth.redirect_url host %q",
					cfg.Auth.CookieDomain, redirectHost,
				)
			}
			cfg.Auth.CookieDomain = cookieDomain
			for _, r := range cfg.Routes {
				if r.RequiresAuth() && !cookieDomainCovers(r.Host, cookieDomain) {
					return nil, fmt.Errorf(
						"config: auth.cookie_domain %q does not cover protected route host %q",
						cfg.Auth.CookieDomain, r.Host,
					)
				}
			}
		} else {
			for _, r := range cfg.Routes {
				if r.RequiresAuth() && hostnameOnly(r.Host) != redirectHost {
					return nil, fmt.Errorf(
						"config: route %q needs auth but is on a different host than auth.redirect_url (%s); set auth.cookie_domain to share the session across hosts",
						r.Host, redirectHost,
					)
				}
			}
		}
	}

	return &cfg, nil
}

func hostOf(rawURL string) (string, error) {
	u, err := ParseTarget(rawURL)
	if err != nil {
		return "", err
	}
	host, err := NormalizeHost(u.Hostname())
	if err != nil {
		return "", err
	}
	return host, nil
}

// Clone returns a deep copy suitable for independent mutation.
func Clone(source Config) Config {
	clone := source
	clone.TLS.Domains = slices.Clone(source.TLS.Domains)
	clone.TrustedProxies = slices.Clone(source.TrustedProxies)
	if source.RateLimit != nil {
		limit := *source.RateLimit
		clone.RateLimit = &limit
	}
	clone.Routes = make([]Route, len(source.Routes))
	for i, route := range source.Routes {
		clone.Routes[i] = route
		if route.Access != nil {
			access := *route.Access
			access.AllowedEmails = slices.Clone(route.Access.AllowedEmails)
			access.AllowedDomains = slices.Clone(route.Access.AllowedDomains)
			access.RequiredGroups = slices.Clone(route.Access.RequiredGroups)
			clone.Routes[i].Access = &access
		}
	}
	return clone
}
