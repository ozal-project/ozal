// Package config loads Switchboard's routing table from a JSON file.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const DefaultAdminListen = "127.0.0.1:9090"

type Route struct {
	Host         string        `json:"host"`
	Target       string        `json:"target,omitempty"`
	Connector    string        `json:"connector,omitempty"`
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
	if r.Connector != "" {
		return "connector:" + r.Connector
	}
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
	// OriginClientCAFile is a PEM trust bundle. When set, the public HTTPS
	// listener serves only connections that present a client certificate
	// issued by it (Authenticated Origin Pulls with the zone's own
	// certificate). Changing it requires a restart.
	OriginClientCAFile string `json:"origin_client_ca_file,omitempty"`
	// DNS01 switches the public listener to Switchboard's own bounded ACME
	// DNS-01 manager (EDGE-007): no port 80 and no inbound validation, one
	// hostname, Cloudflare DNS. Nil keeps the autocert HTTP-01 path.
	DNS01 *DNS01 `json:"dns01,omitempty"`
}

// DNS01 configures the own certificate manager. DNSToken is a secret
// reference (file: or env:) for a zone-scoped API token; the resolved value
// never reaches config, logs or events.
type DNS01 struct {
	// CertificateRootsFile optionally supplies explicit roots for staging certificates.
	CertificateRootsFile string `json:"certificate_roots_file,omitempty"`
	StateDir             string `json:"state_dir"`
	DirectoryURL         string `json:"directory_url"`
	DNSToken             string `json:"dns_token"`
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

// Notify configures outbound notifications (NTF-001). Channels are opt-in:
// a nil Notify means Switchboard publishes events to its log and console
// only.
type Notify struct {
	Telegram *Telegram `json:"telegram,omitempty"`
}

// Telegram configures the bot channel. Token is a secret reference (env: or
// file:); ChatsFile stores the paired chat IDs and must be writable by the
// process. APIBase overrides the Bot API root for a self-hosted local Bot
// API server; it must be https, or http only on loopback.
type Telegram struct {
	Token     string `json:"token"`
	ChatsFile string `json:"chats_file"`
	APIBase   string `json:"api_base,omitempty"`
}

// Limit is a token bucket: Rate requests per second with bursts up to Burst.
type Limit struct {
	Rate  float64 `json:"rate"`
	Burst int     `json:"burst"`
}

// RateLimit throttles requests per client IP and route. General applies to
// every request; Auth applies in addition to paths under /_/auth/ and should
// be much stricter. Identity, when present, adds one budget per
// authenticated principal, charged only after the access policy allowed the
// request. A nil RateLimit disables limiting.
type RateLimit struct {
	General  Limit  `json:"general"`
	Auth     Limit  `json:"auth"`
	Identity *Limit `json:"identity,omitempty"`
	// MaxKeys caps how many distinct buckets each limiter keeps; zero
	// selects the built-in default. It is the memory bound of the stage: a
	// scan that varies its key cannot grow the maps without limit.
	MaxKeys int `json:"max_keys,omitempty"`
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
	Notify    *Notify    `json:"notify,omitempty"`
	Tunnel    *Tunnel    `json:"tunnel,omitempty"`
	// UpstreamTimeoutSeconds is how long an upstream may take to start
	// answering before the visitor gets a 504. Zero means the default (60);
	// the response body is never limited.
	UpstreamTimeoutSeconds int     `json:"upstream_timeout_seconds,omitempty"`
	Routes                 []Route `json:"routes"`
}

const (
	// DefaultUpstreamTimeout applies when upstream_timeout_seconds is unset.
	DefaultUpstreamTimeout = 60 * time.Second
	// maxUpstreamTimeoutSeconds keeps a typo from disabling the protection.
	maxUpstreamTimeoutSeconds = 3600
	// minRateLimitKeys and maxRateLimitKeys bound rate_limit.max_keys; zero
	// (omitted) selects the ratelimit package's default.
	minRateLimitKeys = 100
	maxRateLimitKeys = 1_000_000
)

// UpstreamTimeout returns the configured response-header limit, or the default.
func (c Config) UpstreamTimeout() time.Duration {
	if c.UpstreamTimeoutSeconds == 0 {
		return DefaultUpstreamTimeout
	}
	return time.Duration(c.UpstreamTimeoutSeconds) * time.Second
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
		limits := map[string]Limit{"general": cfg.RateLimit.General, "auth": cfg.RateLimit.Auth}
		if cfg.RateLimit.Identity != nil {
			limits["identity"] = *cfg.RateLimit.Identity
		}
		for name, l := range limits {
			if l.Rate <= 0 || l.Burst < 1 {
				return nil, fmt.Errorf("config: rate_limit.%s needs rate > 0 and burst >= 1", name)
			}
		}
		if n := cfg.RateLimit.MaxKeys; n != 0 && (n < minRateLimitKeys || n > maxRateLimitKeys) {
			return nil, fmt.Errorf("config: rate_limit.max_keys must be between %d and %d (or omitted)", minRateLimitKeys, maxRateLimitKeys)
		}
	}
	if cfg.UpstreamTimeoutSeconds < 0 || cfg.UpstreamTimeoutSeconds > maxUpstreamTimeoutSeconds {
		return nil, fmt.Errorf("config: upstream_timeout_seconds must be between 0 (default) and %d", maxUpstreamTimeoutSeconds)
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
	if err := validateTunnel(cfg.Tunnel); err != nil {
		return nil, err
	}
	seenHosts := make(map[string]struct{}, len(cfg.Routes))
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.Host == "" {
			return nil, fmt.Errorf("config: route missing host: %+v", *r)
		}
		destinations := 0
		for _, value := range []string{r.Target, r.Root, r.Connector} {
			if value != "" {
				destinations++
			}
		}
		if destinations != 1 {
			return nil, fmt.Errorf("config: route %q needs exactly one of target, root or connector", r.Host)
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
		if r.Connector != "" {
			if cfg.Tunnel == nil {
				return nil, fmt.Errorf("config: connector route %q requires tunnel settings", host)
			}
			if err := ValidateConnectorName(r.Connector); err != nil {
				return nil, err
			}
			if r.RequiresAuth() {
				return nil, fmt.Errorf("config: protected connector routes require the visitor identity contract (not implemented)")
			}
			if r.HealthPath != "" || r.SPA {
				return nil, fmt.Errorf("config: connector route %q cannot set health_path or spa", host)
			}
			continue
		}
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
		if cfg.TLS.DNS01 == nil {
			for _, domain := range cfg.TLS.Domains {
				if strings.Contains(domain, "*") {
					return nil, fmt.Errorf("config: wildcard domain %q requires tls.dns01 (autocert cannot issue wildcards)", domain)
				}
			}
			if cfg.TLS.CacheDir == "" {
				return nil, fmt.Errorf("config: tls.enabled requires cache_dir")
			}
		} else {
			if err := normalizeDNS01(&cfg.TLS); err != nil {
				return nil, err
			}
		}
	} else if cfg.TLS.OriginClientCAFile != "" {
		return nil, fmt.Errorf("config: tls.origin_client_ca_file requires tls.enabled")
	} else if cfg.TLS.DNS01 != nil {
		return nil, fmt.Errorf("config: tls.dns01 requires tls.enabled")
	}

	if err := ValidateSecretReference("admin.token", cfg.Admin.Token); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if cfg.Notify != nil {
		if cfg.Notify.Telegram == nil {
			return nil, fmt.Errorf("config: notify requires telegram")
		}
		telegram := cfg.Notify.Telegram
		if strings.TrimSpace(telegram.Token) == "" {
			return nil, fmt.Errorf("config: notify.telegram requires token")
		}
		if err := ValidateSecretReference("notify.telegram.token", telegram.Token); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		if telegram.ChatsFile == "" {
			return nil, fmt.Errorf("config: notify.telegram requires chats_file")
		}
		if !filepath.IsAbs(telegram.ChatsFile) {
			return nil, fmt.Errorf("config: notify.telegram chats_file %q must be an absolute path", telegram.ChatsFile)
		}
		telegram.ChatsFile = filepath.Clean(telegram.ChatsFile)
		if telegram.APIBase != "" {
			parsed, err := url.Parse(telegram.APIBase)
			if err != nil || parsed.Host == "" {
				return nil, fmt.Errorf("config: notify.telegram api_base %q is not a URL", telegram.APIBase)
			}
			loopback := parsed.Hostname() == "localhost" || net.ParseIP(parsed.Hostname()) != nil && net.ParseIP(parsed.Hostname()).IsLoopback()
			if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
				return nil, fmt.Errorf("config: notify.telegram api_base must be https (http only for a loopback Bot API server)")
			}
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

// normalizeDNS01 validates the own-manager TLS configuration and normalizes
// the single name it serves (a hostname or one wildcard). The scope is one name and
// one state directory: staging and production are separated by pointing the
// directory elsewhere, not by sharing one registration.
func normalizeDNS01(tls *TLS) error {
	if len(tls.Domains) != 1 {
		return fmt.Errorf("config: tls.dns01 supports exactly one domain today, got %d", len(tls.Domains))
	}
	// A single-level wildcard ("*.example.com") is a plain hostname with one
	// leading label that stands for any single subdomain; the rest is
	// validated like any other host.
	wildcard := strings.HasPrefix(tls.Domains[0], "*.")
	host, err := NormalizeHost(strings.TrimPrefix(tls.Domains[0], "*."))
	if err != nil || strings.Contains(host, ":") {
		return fmt.Errorf("config: tls.domains[0] %q is not a plain hostname", tls.Domains[0])
	}
	if wildcard {
		if !strings.Contains(host, ".") {
			return fmt.Errorf("config: tls.domains[0] %q: a wildcard must sit above a registered domain", tls.Domains[0])
		}
		host = "*." + host
	}
	tls.Domains[0] = host

	dns01 := tls.DNS01
	if dns01.StateDir == "" {
		return fmt.Errorf("config: tls.dns01 requires state_dir")
	}
	if !filepath.IsAbs(dns01.StateDir) {
		return fmt.Errorf("config: tls.dns01 state_dir %q must be an absolute path", dns01.StateDir)
	}
	dns01.StateDir = filepath.Clean(dns01.StateDir)
	if dns01.CertificateRootsFile != "" {
		if !filepath.IsAbs(dns01.CertificateRootsFile) {
			return fmt.Errorf("config: tls.dns01 certificate_roots_file must be an absolute path")
		}
		dns01.CertificateRootsFile = filepath.Clean(dns01.CertificateRootsFile)
	}
	if tls.Email == "" {
		return fmt.Errorf("config: tls.dns01 requires tls.email for the ACME account")
	}
	if dns01.DirectoryURL == "" {
		return fmt.Errorf("config: tls.dns01 requires directory_url")
	}
	parsed, err := url.Parse(dns01.DirectoryURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("config: tls.dns01 directory_url %q must be an absolute https URL", dns01.DirectoryURL)
	}
	if strings.TrimSpace(dns01.DNSToken) == "" {
		return fmt.Errorf("config: tls.dns01 requires dns_token")
	}
	if err := ValidateSecretReference("tls.dns01.dns_token", dns01.DNSToken); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
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
	if source.Tunnel != nil {
		tunnel := *source.Tunnel
		clone.Tunnel = &tunnel
	}
	if source.TLS.DNS01 != nil {
		dns01 := *source.TLS.DNS01
		clone.TLS.DNS01 = &dns01
	}
	clone.TrustedProxies = slices.Clone(source.TrustedProxies)
	if source.Notify != nil {
		notifyCopy := *source.Notify
		if source.Notify.Telegram != nil {
			telegramCopy := *source.Notify.Telegram
			notifyCopy.Telegram = &telegramCopy
		}
		clone.Notify = &notifyCopy
	}
	if source.RateLimit != nil {
		limit := *source.RateLimit
		if source.RateLimit.Identity != nil {
			identity := *source.RateLimit.Identity
			limit.Identity = &identity
		}
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
