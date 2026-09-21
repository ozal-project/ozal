// Package config loads Switchboard's routing table from a JSON file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Route struct {
	Host         string        `json:"host"`
	Target       string        `json:"target"`
	HealthPath   string        `json:"health_path,omitempty"`   // defaults to "/" if empty
	AuthRequired bool          `json:"auth_required,omitempty"` // legacy shorthand for access: {}
	Access       *AccessPolicy `json:"access,omitempty"`
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

type Config struct {
	Listen string  `json:"listen"`
	TLS    TLS     `json:"tls,omitempty"`
	Auth   Auth    `json:"auth,omitempty"`
	Routes []Route `json:"routes"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Listen == "" {
		return nil, fmt.Errorf("config: listen address is required")
	}
	if len(cfg.Routes) == 0 {
		return nil, fmt.Errorf("config: at least one route is required")
	}
	seenHosts := make(map[string]struct{}, len(cfg.Routes))
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.Host == "" || r.Target == "" {
			return nil, fmt.Errorf("config: route missing host or target: %+v", *r)
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
		if _, err := ParseTarget(r.Target); err != nil {
			return nil, fmt.Errorf("config: route %q has invalid target %q: %w", r.Host, r.Target, err)
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
