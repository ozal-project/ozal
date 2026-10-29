// Package config loads Ozal's routing table from a JSON file.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const DefaultAdminListen = "127.0.0.1:9090"

type Route struct {
	Host         string        `json:"host"`
	Target       string        `json:"target,omitempty"`
	Upstreams    []string      `json:"upstreams,omitempty"`
	Connector    string        `json:"connector,omitempty"`
	Root         string        `json:"root,omitempty"`          // static site directory; alternative to Target
	Site         string        `json:"site,omitempty"`          // deployed site (pages.dir); alternative to Root
	SPA          bool          `json:"spa,omitempty"`           // static only: unknown paths fall back to index.html
	HealthPath   string        `json:"health_path,omitempty"`   // defaults to "/" if empty
	AuthRequired bool          `json:"auth_required,omitempty"` // legacy shorthand for access: {}
	Access       *AccessPolicy `json:"access,omitempty"`
	// Resource is an owner-declared catalog entry. It is descriptive evidence;
	// labels do not change access decisions unless a later policy explicitly uses them.
	Resource *RouteResource `json:"resource,omitempty"`
	// AuditMode controls synchronous append behavior for access events when
	// top-level audit settings are enabled. Empty selects required for protected
	// routes and best_effort for public routes.
	AuditMode string `json:"audit_mode,omitempty"`
}

// RouteResource describes what a route protects. Access remains the route's
// default authorization requirement; this catalog adds ownership and data
// labels without guessing them from the hostname or upstream.
type RouteResource struct {
	Owner        string   `json:"owner"`
	DataClass    string   `json:"data_class"`
	Sensitivity  string   `json:"sensitivity"`
	RiskyActions []string `json:"risky_actions,omitempty"`
}

// AuditConfig enables the local durable audit adapter for the explicitly
// selected event families. The first supported family is access decisions.
type AuditConfig struct {
	Dir             string   `json:"dir"`
	Include         []string `json:"include"`
	AdminMode       string   `json:"admin_mode,omitempty"`
	MaxSegmentBytes int64    `json:"max_segment_bytes,omitempty"`
}

// AdminRequired selects fail-closed behavior for admin mutation intent
// records. It is consulted only when audit.include contains "admin".
func (a AuditConfig) AdminRequired() bool { return a.AdminMode != "best_effort" }

// Includes reports whether the durable audit configuration selects a family.
func (a AuditConfig) Includes(family string) bool {
	for _, configured := range a.Include {
		if configured == family {
			return true
		}
	}
	return false
}

// IsStatic reports whether the route serves files (a fixed Root or a
// deployed Site) instead of proxying to Target.
func (r Route) IsStatic() bool {
	return r.Root != "" || r.Site != ""
}

// Destination is the human-readable upstream: the target URL, or the site
// directory for static routes.
func (r Route) Destination() string {
	if r.Connector != "" {
		return "connector:" + r.Connector
	}
	if len(r.Upstreams) > 0 {
		return strings.Join(r.Upstreams, ", ")
	}
	if r.Site != "" {
		return "site:" + r.Site
	}
	if r.IsStatic() {
		return r.Root
	}
	return r.Target
}

// UpstreamTargets returns the HTTP targets for this route. The returned slice
// is independent of the route so callers can safely retain or modify it.
func (r Route) UpstreamTargets() []string {
	if len(r.Upstreams) > 0 {
		return slices.Clone(r.Upstreams)
	}
	if r.Target != "" {
		return []string{r.Target}
	}
	return nil
}

// RequiresAuth reports whether a route needs an authenticated identity.
// Access being present implies authentication even when its policy is empty.
func (r Route) RequiresAuth() bool {
	return r.AuthRequired || r.Access != nil
}

// AuditRequired reports whether an access event must be durable before the
// request receives its response or reaches the upstream. Callers use this
// only when top-level audit configuration enables the access event family.
func (r Route) AuditRequired() bool {
	switch r.AuditMode {
	case "required":
		return true
	case "best_effort":
		return false
	default:
		return r.RequiresAuth()
	}
}

// Policy returns the access policy enforced on a protected route: the
// configured one, or the empty policy (any authenticated user) for the legacy
// AuthRequired shorthand. It is meaningless for a public route; check
// RequiresAuth first. The proxy and `policy explain` both use it so they cannot
// diverge.
func (r Route) Policy() AccessPolicy {
	if r.Access != nil {
		return *r.Access
	}
	return AccessPolicy{}
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
	// DNS01 switches the public listener to Ozal's own bounded ACME
	// DNS-01 manager (EDGE-007): no port 80 and no inbound validation. The
	// DNS provider is Cloudflare (default) or any server that accepts
	// signed RFC 2136 updates. Nil keeps the autocert HTTP-01 path.
	DNS01 *DNS01 `json:"dns01,omitempty"`
}

// DNS01 configures the own certificate manager. DNSToken is a secret
// reference (file: or env:) for a zone-scoped API token; the resolved value
// never reaches config, logs or events. DNSProvider selects the adapter:
// "cloudflare" (default, needs DNSToken) or "rfc2136" (needs RFC2136 and
// refuses DNSToken) or "hetzner" (needs Hetzner and refuses DNSToken). The
// ACME flow does not change with the provider.
type DNS01 struct {
	// CertificateRootsFile optionally supplies explicit roots for staging certificates.
	CertificateRootsFile string        `json:"certificate_roots_file,omitempty"`
	StateDir             string        `json:"state_dir"`
	DirectoryURL         string        `json:"directory_url"`
	DNSProvider          string        `json:"dns_provider,omitempty"`
	DNSToken             string        `json:"dns_token,omitempty"`
	RFC2136              *DNS01RFC2136 `json:"rfc2136,omitempty"`
	Hetzner              *DNS01Hetzner `json:"hetzner,omitempty"`
	// ChallengeDelegation maps a certificate domain to the name where its
	// DNS-01 TXT is written. The operator creates one CNAME,
	// _acme-challenge.<domain> -> target, at whatever DNS serves the domain;
	// only the zone holding the target then needs a provider. Whoever
	// controls the target zone can obtain certificates for the domain.
	ChallengeDelegation map[string]string `json:"challenge_delegation,omitempty"`
	// Builtin configures Ozal's own authoritative DNS server, used with
	// dns_provider "builtin": no provider account or API token is involved.
	Builtin *DNS01Builtin `json:"builtin,omitempty"`
	// ExternalAccount carries the credentials some CAs (Google Trust
	// Services, ZeroSSL) require to register an ACME account.
	ExternalAccount *DNS01ExternalAccount `json:"external_account,omitempty"`
}

// DNS01ExternalAccount is the ACME external account binding (RFC 8555
// section 7.3.4). HMACKey is an env: or file: reference to the base64url key
// the CA issued with KeyID; the value never reaches config. It is read only
// when the account is first registered.
type DNS01ExternalAccount struct {
	KeyID   string `json:"key_id"`
	HMACKey string `json:"hmac_key"`
}

// DNS01Builtin makes Ozal answer DNS for one zone that only holds challenge
// records. The parent zone delegates it with NS records, and each domain
// points its _acme-challenge name at a name inside it with one CNAME.
// Listen is an explicit IP:port (53 for public use); Nameservers are the host
// names the parent zone delegates to.
type DNS01Builtin struct {
	Zone        string   `json:"zone"`
	Listen      string   `json:"listen"`
	Nameservers []string `json:"nameservers"`
	Contact     string   `json:"contact,omitempty"`
}

// DNS01Hetzner selects the Hetzner Cloud DNS API for one explicit zone.
// Token is a file: or env: reference to an API token; the value never
// reaches config.
type DNS01Hetzner struct {
	Zone  string `json:"zone"`
	Token string `json:"token"`
}

// DNS01RFC2136 points at an authoritative server that accepts TSIG-signed
// dynamic updates (BIND, PowerDNS, Knot). Secret is a file: or env:
// reference to the base64 TSIG secret; the value never reaches config.
type DNS01RFC2136 struct {
	Server    string `json:"server"`
	Zone      string `json:"zone"`
	KeyName   string `json:"key_name"`
	Algorithm string `json:"algorithm,omitempty"`
	Secret    string `json:"secret"`
}

// DNS describes how visitors' DNS reaches this edge, so the console can name
// the one record a new hostname needs and check it (UI-006). Target is this
// edge's public address: an IPv4/IPv6 address (A/AAAA record) or a hostname
// (CNAME). Proxied marks records created through the provider as going
// through its proxy. Cloudflare is optional: with it the console can create a
// missing record; without it every record is added by hand.
type DNS struct {
	Target     string         `json:"target,omitempty"`
	Proxied    bool           `json:"proxied,omitempty"`
	Cloudflare *DNSCloudflare `json:"cloudflare,omitempty"`
}

// DNSCloudflare holds a secret reference (file: or env:) to a token limited
// to DNS edit on the zones it serves. It is resolved only when a record is
// created and never written back.
type DNSCloudflare struct {
	Token string `json:"token"`
}

type Auth struct {
	Issuer       string `json:"issuer,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	RedirectURL  string `json:"redirect_url,omitempty"`
	// SessionTTLMinutes is the absolute session lifetime (default 10080, 7
	// days); SessionIdleMinutes ends a session unused for that long
	// (default 720, 12 h). Zero means the default.
	SessionTTLMinutes  int `json:"session_ttl_minutes,omitempty"`
	SessionIdleMinutes int `json:"session_idle_minutes,omitempty"`

	// CookieDomain, if set, shares the session cookie across every
	// subdomain of it (e.g. ".example.com" covers magiclink.example.com
	// and auditanchor.example.com alike). With an external OIDC login only,
	// it is required if a protected route is on a different host than
	// redirect_url's — otherwise the session set during the OIDC callback
	// (which always happens on redirect_url's host) won't be sent back on
	// the other hosts' requests, and they'll redirect to login in a loop.
	// With the passkey login, a protected host it does not cover gets its
	// own host session from the login instead (ID-015).
	CookieDomain string `json:"cookie_domain,omitempty"`

	// ProviderName labels the OIDC button on the passkey login page when both
	// are configured ("Google", "Okta"); defaults to "single sign-on".
	ProviderName string `json:"provider_name,omitempty"`

	// Passkeys makes Ozal authenticate visitors itself (ID-003). The
	// OIDC fields may be set as well (ID-007): the login page then offers
	// both, and redirect_url must be login_url + "/_/auth/callback".
	Passkeys *Passkeys `json:"passkeys,omitempty"`
}

// Passkeys configures native WebAuthn login. Users and their passkeys live
// in the state database (-db); only the operator can invite them.
type Passkeys struct {
	// RPID is the domain the passkeys belong to: the login host or a parent
	// of it ("example.com" covers auth.example.com and app.example.com).
	RPID string `json:"rp_id"`
	// LoginURL is the origin where the login and enrolment pages run, e.g.
	// "https://auth.example.com". Browsers sign for this origin only.
	LoginURL string `json:"login_url"`
	// DisplayName is what the authenticator shows; "Ozal" if empty.
	DisplayName string `json:"display_name,omitempty"`
}

type Admin struct {
	Listen string `json:"listen,omitempty"`
	Token  string `json:"token,omitempty"`
	// SitesToken is a narrower bearer that can deploy and roll back sites and
	// nothing else, for the machines that publish them (2026-10-21). A
	// secret reference like Token.
	SitesToken string `json:"sites_token,omitempty"`
	// Socket is an optional unix socket for the admin API (CLI-001). The
	// file mode (0600) is its credential: a request on it acts as the host
	// operator, needs no bearer and may run host-only operations.
	Socket string `json:"socket,omitempty"`
}

// Notify configures outbound notifications (NTF-001). Channels are opt-in:
// a nil Notify means Ozal publishes events to its log and console
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

// Pages enables deployed static sites (PAGES-002): archives uploaded
// through the admin API are stored under Dir, and a route with "site"
// serves the live deployment. Keep is how many deployments per site are
// retained (zero selects the default). Changing it requires a restart.
type Pages struct {
	Dir    string          `json:"dir"`
	Keep   int             `json:"keep,omitempty"`
	Deploy *DeployEndpoint `json:"deploy,omitempty"`
}

// siteNamePattern: a site name becomes a directory name, so it follows the
// same DNS-label rule as connector names (and pages.ValidSiteName).
var siteNamePattern = connectorNamePattern

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
	RuntimeFile string     `json:"runtime_file,omitempty"`
	GitHub      *GitHubApp `json:"github,omitempty"`
	Listen      string     `json:"listen"`
	TLS         TLS        `json:"tls,omitempty"`
	Auth        Auth       `json:"auth,omitempty"`
	Admin       Admin      `json:"admin,omitempty"`
	// TrustedProxies opts specific TCP peer IPs or CIDRs into CF-Connecting-IP.
	// Empty means no forwarding peer is trusted.
	TrustedProxies []string `json:"trusted_proxies,omitempty"`
	// DevErrors renders local-development error pages (upstream address,
	// hint, route list on 404). Local-only: it reveals internal addresses.
	DevErrors bool         `json:"dev_errors,omitempty"`
	RateLimit *RateLimit   `json:"rate_limit,omitempty"`
	Notify    *Notify      `json:"notify,omitempty"`
	Audit     *AuditConfig `json:"audit,omitempty"`
	Tunnel    *Tunnel      `json:"tunnel,omitempty"`
	Pages     *Pages       `json:"pages,omitempty"`
	DNS       *DNS         `json:"dns,omitempty"`
	Projects  []Project    `json:"projects,omitempty"`
	// UpstreamTimeoutSeconds is how long an upstream may take to start
	// answering before the visitor gets a 504. Zero means the default (60);
	// the response body is never limited.
	UpstreamTimeoutSeconds int     `json:"upstream_timeout_seconds,omitempty"`
	Routes                 []Route `json:"routes"`
}

const (
	// DefaultUpstreamTimeout applies when upstream_timeout_seconds is unset.
	DefaultUpstreamTimeout = 60 * time.Second
	// maxPagesKeep bounds pages.keep: every retained deployment is a full copy.
	maxPagesKeep = 100
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
	cfg, _, err := LoadWithDigest(path)
	return cfg, err
}

// LoadWithDigest is Load that also names the document it parsed: the
// SHA-256 of its bytes (see Digest).
func LoadWithDigest(path string) (*Config, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, "", err
	}
	return loadOperating(cfg, data)
}

// Digest names a configuration document by the hex SHA-256 of its bytes, so
// a deploy tool can confirm which file the running process actually loaded,
// not only that some reload happened.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// FileDigest is Digest of the file at path as it is on disk now.
func FileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read config: %w", err)
	}
	return Digest(data), nil
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
	if cfg.RuntimeFile != "" && (!filepath.IsAbs(cfg.RuntimeFile) || filepath.Clean(cfg.RuntimeFile) != cfg.RuntimeFile) {
		return nil, fmt.Errorf("config: runtime_file must be an absolute, clean path")
	}
	if err := cfg.GitHub.Validate(); err != nil {
		return nil, err
	}
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
	if err := validateAdminSocket(cfg.Admin.Socket); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if len(cfg.Routes) == 0 {
		return nil, fmt.Errorf("config: at least one route is required")
	}
	if err := validateTunnel(cfg.Tunnel); err != nil {
		return nil, err
	}
	if cfg.Pages != nil {
		if !filepath.IsAbs(cfg.Pages.Dir) {
			return nil, fmt.Errorf("config: pages.dir %q must be an absolute path", cfg.Pages.Dir)
		}
		if cfg.Pages.Keep < 0 || cfg.Pages.Keep > maxPagesKeep {
			return nil, fmt.Errorf("config: pages.keep must be between 0 (default) and %d", maxPagesKeep)
		}
		cfg.Pages.Dir = filepath.Clean(cfg.Pages.Dir)
	}
	if cfg.Audit != nil {
		if cfg.Audit.Dir == "" || !filepath.IsAbs(cfg.Audit.Dir) {
			return nil, fmt.Errorf("config: audit.dir must be an absolute path")
		}
		cfg.Audit.Dir = filepath.Clean(cfg.Audit.Dir)
		if cfg.Audit.MaxSegmentBytes < 0 || cfg.Audit.MaxSegmentBytes > 1<<40 {
			return nil, fmt.Errorf("config: audit.max_segment_bytes must be between 0 (default) and %d", int64(1<<40))
		}
		if len(cfg.Audit.Include) == 0 {
			return nil, fmt.Errorf("config: audit.include must select at least one event family")
		}
		seenAuditFamilies := make(map[string]struct{}, len(cfg.Audit.Include))
		for _, family := range cfg.Audit.Include {
			if family != "access" && family != "admin" {
				return nil, fmt.Errorf("config: audit.include contains unsupported event family %q", family)
			}
			if _, exists := seenAuditFamilies[family]; exists {
				return nil, fmt.Errorf("config: audit.include repeats %q", family)
			}
			seenAuditFamilies[family] = struct{}{}
		}
		if cfg.Audit.AdminMode != "" {
			if _, enabled := seenAuditFamilies["admin"]; !enabled {
				return nil, fmt.Errorf("config: audit.admin_mode requires audit.include to contain admin")
			}
			if cfg.Audit.AdminMode != "required" && cfg.Audit.AdminMode != "best_effort" {
				return nil, fmt.Errorf("config: audit.admin_mode must be required or best_effort")
			}
		}
	}
	seenHosts := make(map[string]struct{}, len(cfg.Routes))
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.Host == "" {
			return nil, fmt.Errorf("config: route missing host: %+v", *r)
		}
		destinations := 0
		for _, value := range []string{r.Target, r.Root, r.Connector, r.Site} {
			if value != "" {
				destinations++
			}
		}
		if r.Upstreams != nil {
			destinations++
		}
		if destinations != 1 {
			return nil, fmt.Errorf("config: route %q needs exactly one of target, upstreams, root, site or connector", r.Host)
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
		if r.AuditMode != "" {
			if cfg.Audit == nil {
				return nil, fmt.Errorf("config: route %q audit_mode requires top-level audit settings", host)
			}
			if !cfg.Audit.Includes("access") {
				return nil, fmt.Errorf("config: route %q audit_mode requires audit.include to contain access", host)
			}
			if r.AuditMode != "required" && r.AuditMode != "best_effort" {
				return nil, fmt.Errorf("config: route %q audit_mode must be required or best_effort", host)
			}
			if r.RequiresAuth() && r.AuditMode == "best_effort" {
				return nil, fmt.Errorf("config: protected route %q cannot set audit_mode to best_effort", host)
			}
		}
		if err := validateRouteResource(host, r.Resource); err != nil {
			return nil, err
		}
		if r.Connector != "" {
			if cfg.Tunnel == nil {
				return nil, fmt.Errorf("config: connector route %q requires tunnel settings", host)
			}
			if err := ValidateConnectorName(r.Connector); err != nil {
				return nil, err
			}
			if r.HealthPath != "" || r.SPA {
				return nil, fmt.Errorf("config: connector route %q cannot set health_path or spa", host)
			}
			continue
		}
		if r.Site != "" {
			if cfg.Pages == nil {
				return nil, fmt.Errorf("config: site route %q requires pages settings", host)
			}
			if !siteNamePattern.MatchString(r.Site) {
				return nil, fmt.Errorf("config: route %q site name must be 1-63 characters of a-z, 0-9 or '-' with alphanumeric ends", host)
			}
			if r.HealthPath != "" {
				return nil, fmt.Errorf("config: route %q serves a site and cannot set health_path", host)
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
		if r.Target != "" {
			if _, err := ParseTarget(r.Target); err != nil {
				return nil, fmt.Errorf("config: route %q has invalid target %q: %w", r.Host, r.Target, err)
			}
		}
		if r.Upstreams != nil {
			if len(r.Upstreams) < 2 {
				return nil, fmt.Errorf("config: route %q upstreams must contain at least two targets", r.Host)
			}
			seenTargets := make(map[string]struct{}, len(r.Upstreams))
			for i, target := range r.Upstreams {
				parsed, err := ParseTarget(target)
				if err != nil {
					return nil, fmt.Errorf("config: route %q has invalid upstream at index %d: %w", r.Host, i, err)
				}
				canonical := parsed.String()
				if _, exists := seenTargets[canonical]; exists {
					return nil, fmt.Errorf("config: route %q repeats upstream at index %d", r.Host, i)
				}
				seenTargets[canonical] = struct{}{}
			}
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

	if err := normalizeDNS(cfg.DNS); err != nil {
		return nil, err
	}

	if err := ValidateSecretReference("admin.token", cfg.Admin.Token); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := ValidateSecretReference("admin.sites_token", cfg.Admin.SitesToken); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if cfg.Admin.SitesToken != "" && cfg.Admin.SitesToken == cfg.Admin.Token {
		return nil, fmt.Errorf("config: admin.sites_token must not be the admin token: it exists to be narrower")
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
	for _, r := range cfg.Routes {
		if r.Access == nil {
			continue
		}
		if r.Access.MaxAuthAgeMinutes < 0 {
			return nil, fmt.Errorf("config: route %q: access.max_auth_age_minutes must not be negative", r.Host)
		}
		if r.Access.MaxAuthAgeMinutes > 0 && cfg.Auth.Passkeys == nil {
			return nil, fmt.Errorf("config: route %q: access.max_auth_age_minutes requires auth.passkeys", r.Host)
		}
		if r.Access.RequireDeviceBound && cfg.Auth.Passkeys == nil {
			return nil, fmt.Errorf("config: route %q: access.require_device_bound requires auth.passkeys", r.Host)
		}
		for _, m := range r.Access.Methods {
			if !slices.Contains(cfg.Auth.LoginMethods(), m) {
				return nil, fmt.Errorf("config: route %q: access.methods %q is not a configured login method (have %v)", r.Host, m, cfg.Auth.LoginMethods())
			}
		}
		// A recent or device-bound login is a passkey proof; an upstream
		// provider's session cannot satisfy it.
		if (r.Access.MaxAuthAgeMinutes > 0 || r.Access.RequireDeviceBound) && slices.Contains(r.Access.Methods, MethodOIDC) {
			return nil, fmt.Errorf("config: route %q: max_auth_age_minutes and require_device_bound need a passkey login; remove %q from access.methods", r.Host, MethodOIDC)
		}
		if err := validateContextRule(r, &cfg.Auth); err != nil {
			return nil, err
		}
	}
	if anyProtectedRoute && cfg.Auth.Passkeys != nil {
		loginHost, err := validatePasskeys(&cfg.Auth)
		if err != nil {
			return nil, err
		}
		if err := checkSessionHosts(&cfg, loginHost, "auth.passkeys.login_url", true); err != nil {
			return nil, err
		}
	} else if anyProtectedRoute {
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
		if err := checkSessionHosts(&cfg, redirectHost, "auth.redirect_url", false); err != nil {
			return nil, err
		}
	}

	if err := validateDeployEndpoint(&cfg); err != nil {
		return nil, err
	}
	if len(cfg.Projects) > 0 {
		for i, project := range cfg.Projects {
			for j, deployer := range project.Deployers {
				if deployer.Token == "" {
					continue
				}
				field := fmt.Sprintf("projects[%d].deployers[%d].token", i, j)
				if err := ValidateSecretReference(field, deployer.Token); err != nil {
					return nil, fmt.Errorf("config: %w", err)
				}
			}
		}
		if err := ValidateProjects(cfg.Projects, cfg.Routes); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		for i := range cfg.Projects {
			for j, rawHost := range cfg.Projects[i].Hostnames {
				host, err := NormalizeHost(rawHost)
				if err != nil {
					return nil, fmt.Errorf("config: projects[%d].hostnames[%d] is invalid: %w", i, j, err)
				}
				cfg.Projects[i].Hostnames[j] = host
			}
		}
	}

	return &cfg, nil
}

func validateContextRule(route Route, authConfig *Auth) error {
	rule := route.Access.ContextRule
	if rule == nil {
		return nil
	}
	when := rule.When
	if when.SessionMethod == "" && when.SessionDeviceBound == nil && when.ResourceSensitivity == "" && when.ActionRisky == nil && when.DeviceFirstSeen == nil {
		return fmt.Errorf("config: route %q access.context_rule.when must contain at least one supported condition", route.Host)
	}
	switch rule.Then {
	case "deny", "step_up":
	default:
		return fmt.Errorf("config: route %q access.context_rule.then must be deny or step_up", route.Host)
	}
	if when.SessionMethod != "" {
		if when.SessionMethod != MethodOIDC && when.SessionMethod != MethodPasskey {
			return fmt.Errorf("config: route %q access.context_rule.when.session_method must be oidc or passkey", route.Host)
		}
		if !slices.Contains(authConfig.LoginMethods(), when.SessionMethod) {
			return fmt.Errorf("config: route %q access.context_rule.when.session_method %q is not a configured login method", route.Host, when.SessionMethod)
		}
	}
	if when.ResourceSensitivity != "" {
		if route.Resource == nil {
			return fmt.Errorf("config: route %q access.context_rule.when.resource_sensitivity requires route.resource", route.Host)
		}
		if when.ResourceSensitivity != "public" && when.ResourceSensitivity != "internal" && when.ResourceSensitivity != "confidential" && when.ResourceSensitivity != "restricted" {
			return fmt.Errorf("config: route %q access.context_rule.when.resource_sensitivity is not a supported label", route.Host)
		}
	}
	if when.ActionRisky != nil && (route.Resource == nil || route.Resource.RiskyActions == nil) {
		return fmt.Errorf("config: route %q access.context_rule.when.action_risky requires route.resource.risky_actions, including an explicit empty list when no method is risky", route.Host)
	}
	if when.DeviceFirstSeen != nil && authConfig.Passkeys == nil {
		return fmt.Errorf("config: route %q access.context_rule.when.device_first_seen requires auth.passkeys", route.Host)
	}
	if when.DeviceFirstSeen != nil && when.SessionMethod == MethodOIDC {
		return fmt.Errorf("config: route %q access.context_rule.when.device_first_seen cannot use session_method=oidc because OIDC has no passkey history", route.Host)
	}
	if when.DeviceFirstSeen != nil && len(route.Access.Methods) > 0 && !slices.Contains(route.Access.Methods, MethodPasskey) {
		return fmt.Errorf("config: route %q access.context_rule.when.device_first_seen requires access.methods to allow passkey", route.Host)
	}
	if rule.Then == "step_up" {
		// An OIDC session can satisfy a passkey step-up by changing method.
		// A newly seen passkey can also step up once: its first-seen marker is
		// consumed by the assertion, so the replacement session no longer
		// matches the same rule and cannot loop.
		if when.SessionMethod != MethodOIDC && !(when.DeviceFirstSeen != nil && *when.DeviceFirstSeen) {
			return fmt.Errorf("config: route %q access.context_rule.then step_up requires when.session_method=oidc or when.device_first_seen=true so a successful passkey login can satisfy the rule", route.Host)
		}
		if authConfig.Passkeys == nil || !slices.Contains(authConfig.LoginMethods(), MethodPasskey) {
			return fmt.Errorf("config: route %q access.context_rule.then step_up requires configured passkey login", route.Host)
		}
		if len(route.Access.Methods) > 0 && !slices.Contains(route.Access.Methods, MethodPasskey) {
			return fmt.Errorf("config: route %q access.context_rule.then step_up requires access.methods to allow passkey", route.Host)
		}
	}
	return nil
}

func validateRouteResource(host string, resource *RouteResource) error {
	if resource == nil {
		return nil
	}
	for _, field := range []struct{ name, value string }{
		{"owner", resource.Owner},
		{"data_class", resource.DataClass},
		{"sensitivity", resource.Sensitivity},
	} {
		if field.value == "" || strings.TrimSpace(field.value) != field.value || utf8.RuneCountInString(field.value) > 128 {
			return fmt.Errorf("config: route %q resource.%s must be a non-empty, trimmed label of at most 128 characters", host, field.name)
		}
		for _, r := range field.value {
			if unicode.IsControl(r) {
				return fmt.Errorf("config: route %q resource.%s must not contain control characters", host, field.name)
			}
		}
	}
	switch resource.Sensitivity {
	case "public", "internal", "confidential", "restricted":
	default:
		return fmt.Errorf("config: route %q resource.sensitivity must be public, internal, confidential or restricted", host)
	}
	seen := make(map[string]struct{}, len(resource.RiskyActions))
	for _, action := range resource.RiskyActions {
		if !IsSupportedHTTPMethod(action) {
			return fmt.Errorf("config: route %q resource.risky_actions contains unsupported HTTP method %q", host, action)
		}
		if _, exists := seen[action]; exists {
			return fmt.Errorf("config: route %q resource.risky_actions repeats %q", host, action)
		}
		seen[action] = struct{}{}
	}
	return nil
}

// IsSupportedHTTPMethod reports whether method is one of the HTTP methods
// accepted by route policy, resource catalogs and the policy simulator.
func IsSupportedHTTPMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE":
		return true
	default:
		return false
	}
}

// checkSessionHosts makes sure the session cookie set on loginHost (where
// the login finishes) is sent back on every protected route, and normalizes
// cookie_domain. field names the setting that chose loginHost. With
// handoff (the passkey login, ID-015) a protected host the cookie cannot
// reach is allowed: it gets its own host session after the central login.
func checkSessionHosts(cfg *Config, loginHost, field string, handoff bool) error {
	if cfg.Auth.CookieDomain != "" {
		cookieDomain, err := normalizeCookieDomain(cfg.Auth.CookieDomain)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}
		if !cookieDomainCovers(loginHost, cookieDomain) {
			return fmt.Errorf(
				"config: auth.cookie_domain %q does not cover %s host %q",
				cfg.Auth.CookieDomain, field, loginHost,
			)
		}
		cfg.Auth.CookieDomain = cookieDomain
		if handoff {
			return nil
		}
		for _, r := range cfg.Routes {
			if r.RequiresAuth() && !cookieDomainCovers(r.Host, cookieDomain) {
				return fmt.Errorf(
					"config: auth.cookie_domain %q does not cover protected route host %q",
					cfg.Auth.CookieDomain, r.Host,
				)
			}
		}
		return nil
	}
	if handoff {
		return nil
	}
	for _, r := range cfg.Routes {
		if r.RequiresAuth() && hostnameOnly(r.Host) != loginHost {
			return fmt.Errorf(
				"config: route %q needs auth but is on a different host than %s (%s); set auth.cookie_domain to share the session across hosts",
				r.Host, field, loginHost,
			)
		}
	}
	return nil
}

// validatePasskeys checks auth.passkeys and returns the login hostname. The
// login URL must be a bare origin, HTTPS except on loopback (browsers only
// allow WebAuthn in a secure context), on rp_id or a subdomain of it.
func validatePasskeys(auth *Auth) (string, error) {
	// Normalize a copy: the pointer is shared with the caller's Config.
	copied := *auth.Passkeys
	pk := &copied
	auth.Passkeys = pk
	rpID, err := normalizeHostname(pk.RPID)
	if err != nil || net.ParseIP(rpID) != nil {
		return "", fmt.Errorf("config: auth.passkeys.rp_id %q must be a DNS name", pk.RPID)
	}
	login, err := url.Parse(pk.LoginURL)
	if err != nil || login.Host == "" || login.User != nil || (login.Path != "" && login.Path != "/") || login.RawQuery != "" || login.Fragment != "" {
		return "", fmt.Errorf("config: auth.passkeys.login_url %q must be an origin such as https://auth.example.com", pk.LoginURL)
	}
	loginHost, err := normalizeHostname(login.Hostname())
	if err != nil {
		return "", fmt.Errorf("config: auth.passkeys.login_url %q: %w", pk.LoginURL, err)
	}
	loopback := loginHost == "localhost" || strings.HasSuffix(loginHost, ".localhost")
	if login.Scheme != "https" && !(login.Scheme == "http" && loopback) {
		return "", fmt.Errorf("config: auth.passkeys.login_url must be https (http only on localhost)")
	}
	if loginHost != rpID && !strings.HasSuffix(loginHost, "."+rpID) {
		return "", fmt.Errorf("config: auth.passkeys.login_url host %q is not rp_id %q or a subdomain of it", loginHost, rpID)
	}
	pk.RPID = rpID
	pk.LoginURL = login.Scheme + "://" + login.Host
	if pk.DisplayName == "" {
		pk.DisplayName = "Ozal"
	}
	if err := validateOIDCBesidePasskeys(auth, pk.LoginURL); err != nil {
		return "", err
	}
	return loginHost, nil
}

// validateOIDCBesidePasskeys checks an upstream provider offered next to
// passkeys (ID-007). Either every OIDC field is set or none is, and the
// callback runs on the login host, where the login page and its cookies live.
func validateOIDCBesidePasskeys(auth *Auth, loginURL string) error {
	set := 0
	for _, v := range []string{auth.Issuer, auth.ClientID, auth.ClientSecret, auth.RedirectURL} {
		if v != "" {
			set++
		}
	}
	if set == 0 {
		if auth.ProviderName != "" {
			return fmt.Errorf("config: auth.provider_name names an OIDC provider, but none is configured")
		}
		return nil
	}
	if set != 4 {
		return fmt.Errorf("config: an OIDC provider next to auth.passkeys needs issuer, client_id, client_secret and redirect_url")
	}
	if err := ValidateSecretReference("auth.client_secret", auth.ClientSecret); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if want := loginURL + "/_/auth/callback"; auth.RedirectURL != want {
		return fmt.Errorf("config: with auth.passkeys, auth.redirect_url must be %q (the callback runs on the login host)", want)
	}
	if auth.ProviderName == "" {
		auth.ProviderName = "single sign-on"
	}
	return nil
}

// LoginMethods lists the methods a validated auth config offers.
func (a Auth) LoginMethods() []string {
	var methods []string
	if a.Passkeys != nil {
		methods = append(methods, MethodPasskey)
	}
	if a.Issuer != "" {
		methods = append(methods, MethodOIDC)
	}
	return methods
}

// Login method names, shared by access.methods and the session's amr.
const (
	MethodPasskey = "passkey"
	MethodOIDC    = "oidc"
)

// normalizeCertificateName lowercases one tls.domains entry and checks it.
// A single-level wildcard ("*.example.com") is a plain hostname with one
// leading label that stands for any single subdomain; the rest is validated
// like any other host. Errors read after "tls.domains[i] ".
func normalizeCertificateName(domain string) (string, error) {
	wildcard := strings.HasPrefix(domain, "*.")
	host, err := NormalizeHost(strings.TrimPrefix(domain, "*."))
	if err != nil || strings.Contains(host, ":") {
		return "", fmt.Errorf("%q is not a plain hostname", domain)
	}
	if wildcard {
		if !strings.Contains(host, ".") {
			return "", fmt.Errorf("%q: a wildcard must sit above a registered domain", domain)
		}
		host = "*." + host
	}
	return host, nil
}

// maxDNS01Names bounds the names on the one certificate: each costs an
// authorization on every renewal.
const maxDNS01Names = 4

// normalizeDNS01 validates the own-manager TLS configuration and normalizes
// the names on the one certificate it serves: hostnames or single-level
// wildcards. A wildcard does not cover its own base, so the apex beside
// "*.example.com" is a second name; a name a wildcard in the list already
// covers is refused, as the CA would. The first name is the certificate's
// identity (its storage key): reordering starts a new certificate. The scope
// is one certificate and one state directory: staging and production are
// separated by pointing the directory elsewhere, not by sharing one
// registration.
func normalizeDNS01(tls *TLS) error {
	if len(tls.Domains) == 0 || len(tls.Domains) > maxDNS01Names {
		return fmt.Errorf("config: tls.dns01 takes 1 to %d domains on one certificate, got %d", maxDNS01Names, len(tls.Domains))
	}
	for i, domain := range tls.Domains {
		name, err := normalizeCertificateName(domain)
		if err != nil {
			return fmt.Errorf("config: tls.domains[%d] %w", i, err)
		}
		tls.Domains[i] = name
	}
	for i, name := range tls.Domains {
		for j, other := range tls.Domains {
			if i == j {
				continue
			}
			if name == other {
				return fmt.Errorf("config: tls.domains lists %q twice", name)
			}
			if base, ok := strings.CutPrefix(other, "*."); ok && !strings.HasPrefix(name, "*.") {
				if label, ok := strings.CutSuffix(name, "."+base); ok && label != "" && !strings.Contains(label, ".") {
					return fmt.Errorf("config: tls.domains: %q is already covered by %q", name, other)
				}
			}
		}
	}

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
	if err := normalizeChallengeDelegation(tls); err != nil {
		return err
	}
	if err := normalizeExternalAccount(dns01); err != nil {
		return err
	}
	return normalizeDNS01Provider(tls)
}

func normalizeExternalAccount(dns01 *DNS01) error {
	ea := dns01.ExternalAccount
	if ea == nil {
		return nil
	}
	ea.KeyID = strings.TrimSpace(ea.KeyID)
	if ea.KeyID == "" {
		return fmt.Errorf("config: tls.dns01.external_account requires key_id")
	}
	if !strings.HasPrefix(ea.HMACKey, "env:") && !strings.HasPrefix(ea.HMACKey, "file:") {
		return fmt.Errorf("config: tls.dns01.external_account.hmac_key must be an env: or file: reference, not an inline value")
	}
	if err := ValidateSecretReference("tls.dns01.external_account.hmac_key", ea.HMACKey); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// normalizeChallengeDelegation validates the delegation table: every key is
// the base of a configured certificate name and every target a host name that
// is not the challenge name itself or under it.
func normalizeChallengeDelegation(tls *TLS) error {
	dns01 := tls.DNS01
	if len(dns01.ChallengeDelegation) == 0 {
		dns01.ChallengeDelegation = nil
		return nil
	}
	bases := map[string]bool{}
	for _, domain := range tls.Domains {
		bases[strings.TrimPrefix(domain, "*.")] = true
	}
	out := make(map[string]string, len(dns01.ChallengeDelegation))
	for base, target := range dns01.ChallengeDelegation {
		base = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(base)), ".")
		target = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(target)), ".")
		if !bases[base] {
			return fmt.Errorf("config: tls.dns01.challenge_delegation key %q is not the base of a tls.domains entry", base)
		}
		if target == "" {
			return fmt.Errorf("config: tls.dns01.challenge_delegation target for %q is empty", base)
		}
		host, err := NormalizeHost(target)
		if err != nil || host != target || strings.Contains(target, "*") {
			return fmt.Errorf("config: tls.dns01.challenge_delegation target %q is not a valid host name", target)
		}
		source := "_acme-challenge." + base
		if target == source || strings.HasSuffix(target, "."+source) {
			return fmt.Errorf("config: tls.dns01.challenge_delegation for %q cannot point at itself", base)
		}
		out[base] = target
	}
	dns01.ChallengeDelegation = out
	return nil
}

// challengeHome is the name whose zone must accept the TXT record for a
// certificate name: the delegation target when there is one, else the
// domain itself.
func challengeHome(tls *TLS, domain string) string {
	base := strings.TrimPrefix(domain, "*.")
	if target, ok := tls.DNS01.ChallengeDelegation[base]; ok {
		return target
	}
	return base
}

// normalizeDNS01Provider validates the DNS adapter selection. Each provider
// owns its settings and refuses the other's, so a copied block cannot be
// silently ignored.
func normalizeDNS01Provider(tls *TLS) error {
	dns01 := tls.DNS01
	dns01.DNSProvider = strings.ToLower(strings.TrimSpace(dns01.DNSProvider))
	if dns01.DNSProvider == "" {
		dns01.DNSProvider = "cloudflare"
	}
	switch dns01.DNSProvider {
	case "cloudflare":
		if dns01.RFC2136 != nil {
			return fmt.Errorf("config: tls.dns01.rfc2136 requires dns_provider \"rfc2136\"")
		}
		if dns01.Hetzner != nil {
			return fmt.Errorf("config: tls.dns01.hetzner requires dns_provider \"hetzner\"")
		}
		if dns01.Builtin != nil {
			return fmt.Errorf("config: tls.dns01.builtin requires dns_provider \"builtin\"")
		}
		if strings.TrimSpace(dns01.DNSToken) == "" {
			return fmt.Errorf("config: tls.dns01 requires dns_token")
		}
		if err := ValidateSecretReference("tls.dns01.dns_token", dns01.DNSToken); err != nil {
			return fmt.Errorf("config: %w", err)
		}
		return nil
	case "rfc2136":
		return normalizeRFC2136(tls)
	case "hetzner":
		return normalizeHetzner(tls)
	case "builtin":
		return normalizeBuiltin(tls)
	default:
		return fmt.Errorf("config: tls.dns01 dns_provider %q is not supported (cloudflare, rfc2136, hetzner or builtin)", dns01.DNSProvider)
	}
}

func normalizeBuiltin(tls *TLS) error {
	dns01 := tls.DNS01
	if dns01.DNSToken != "" {
		return fmt.Errorf("config: tls.dns01 dns_token belongs to the cloudflare provider; builtin needs no credential")
	}
	if dns01.RFC2136 != nil {
		return fmt.Errorf("config: tls.dns01.rfc2136 requires dns_provider \"rfc2136\"")
	}
	if dns01.Hetzner != nil {
		return fmt.Errorf("config: tls.dns01.hetzner requires dns_provider \"hetzner\"")
	}
	b := dns01.Builtin
	if b == nil {
		return fmt.Errorf("config: tls.dns01 dns_provider \"builtin\" requires the builtin block")
	}
	b.Zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(b.Zone)), ".")
	if host, err := NormalizeHost(b.Zone); err != nil || host != b.Zone || !strings.Contains(b.Zone, ".") || strings.Contains(b.Zone, "*") {
		return fmt.Errorf("config: tls.dns01.builtin.zone %q must be a domain name with at least two labels", b.Zone)
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(b.Listen))
	addr, parseErr := netip.ParseAddr(host)
	if err != nil || parseErr != nil || addr.IsUnspecified() {
		return fmt.Errorf("config: tls.dns01.builtin.listen must be an explicit IP:port (not a wildcard address or a host name)")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("config: tls.dns01.builtin.listen port is not valid")
	}
	b.Listen = net.JoinHostPort(addr.String(), port)
	if len(b.Nameservers) == 0 || len(b.Nameservers) > 8 {
		return fmt.Errorf("config: tls.dns01.builtin.nameservers takes 1 to 8 host names")
	}
	for i, ns := range b.Nameservers {
		ns = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(ns)), ".")
		if host, err := NormalizeHost(ns); err != nil || host != ns || strings.Contains(ns, "*") {
			return fmt.Errorf("config: tls.dns01.builtin.nameservers[%d] %q is not a valid host name", i, ns)
		}
		b.Nameservers[i] = ns
	}
	if b.Contact = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(b.Contact)), "."); b.Contact != "" {
		if host, err := NormalizeHost(b.Contact); err != nil || host != b.Contact {
			return fmt.Errorf("config: tls.dns01.builtin.contact is not a valid DNS name")
		}
	}
	if len(dns01.ChallengeDelegation) == 0 {
		return fmt.Errorf("config: tls.dns01 dns_provider \"builtin\" requires challenge_delegation: the server only answers for %s", b.Zone)
	}
	for base, target := range dns01.ChallengeDelegation {
		if target == b.Zone || !strings.HasSuffix(target, "."+b.Zone) {
			return fmt.Errorf("config: tls.dns01.challenge_delegation target %q for %q must be a name below the builtin zone %q", target, base, b.Zone)
		}
	}
	for _, domain := range tls.Domains {
		if _, ok := dns01.ChallengeDelegation[strings.TrimPrefix(domain, "*.")]; !ok {
			return fmt.Errorf("config: tls.domains entry %q has no challenge_delegation: the builtin server cannot write records outside its zone", domain)
		}
	}
	return nil
}

func normalizeHetzner(tls *TLS) error {
	dns01 := tls.DNS01
	if dns01.DNSToken != "" {
		return fmt.Errorf("config: tls.dns01 dns_token belongs to the cloudflare provider; hetzner uses hetzner.token")
	}
	if dns01.RFC2136 != nil {
		return fmt.Errorf("config: tls.dns01.rfc2136 requires dns_provider \"rfc2136\"")
	}
	if dns01.Builtin != nil {
		return fmt.Errorf("config: tls.dns01.builtin requires dns_provider \"builtin\"")
	}
	h := dns01.Hetzner
	if h == nil {
		return fmt.Errorf("config: tls.dns01 dns_provider \"hetzner\" requires the hetzner block")
	}
	h.Zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h.Zone)), ".")
	if h.Zone == "" || strings.Contains(h.Zone, "..") || strings.ContainsAny(h.Zone, "/?# ") {
		return fmt.Errorf("config: tls.dns01.hetzner.zone is required")
	}
	if !strings.HasPrefix(h.Token, "env:") && !strings.HasPrefix(h.Token, "file:") {
		return fmt.Errorf("config: tls.dns01.hetzner.token must be an env: or file: reference, not an inline value")
	}
	if err := ValidateSecretReference("tls.dns01.hetzner.token", h.Token); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	for _, domain := range tls.Domains {
		home := challengeHome(tls, domain)
		if home != h.Zone && !strings.HasSuffix(home, "."+h.Zone) {
			return fmt.Errorf("config: tls.dns01.hetzner.zone %q does not contain the challenge name for tls.domains entry %q (%s)", h.Zone, domain, home)
		}
	}
	return nil
}

func normalizeRFC2136(tls *TLS) error {
	dns01 := tls.DNS01
	if dns01.DNSToken != "" {
		return fmt.Errorf("config: tls.dns01 dns_token belongs to the cloudflare provider; rfc2136 uses rfc2136.secret")
	}
	if dns01.Hetzner != nil {
		return fmt.Errorf("config: tls.dns01.hetzner requires dns_provider \"hetzner\"")
	}
	if dns01.Builtin != nil {
		return fmt.Errorf("config: tls.dns01.builtin requires dns_provider \"builtin\"")
	}
	r := dns01.RFC2136
	if r == nil {
		return fmt.Errorf("config: tls.dns01 dns_provider \"rfc2136\" requires the rfc2136 block")
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(r.Server))
	if err != nil || host == "" {
		return fmt.Errorf("config: tls.dns01.rfc2136.server must be host:port")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("config: tls.dns01.rfc2136.server port is not valid")
	}
	r.Server = net.JoinHostPort(host, port)
	r.Zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(r.Zone)), ".")
	if r.Zone == "" || strings.Contains(r.Zone, "..") {
		return fmt.Errorf("config: tls.dns01.rfc2136.zone is required")
	}
	if strings.TrimSpace(r.KeyName) == "" {
		return fmt.Errorf("config: tls.dns01.rfc2136.key_name is required")
	}
	r.Algorithm = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(r.Algorithm)), ".")
	if r.Algorithm == "" {
		r.Algorithm = "hmac-sha256"
	}
	switch r.Algorithm {
	case "hmac-sha256", "hmac-sha384", "hmac-sha512":
	default:
		return fmt.Errorf("config: tls.dns01.rfc2136.algorithm %q is not supported (hmac-sha256, hmac-sha384 or hmac-sha512)", r.Algorithm)
	}
	if strings.TrimSpace(r.Secret) == "" {
		return fmt.Errorf("config: tls.dns01.rfc2136 requires secret")
	}
	if !strings.HasPrefix(r.Secret, "env:") && !strings.HasPrefix(r.Secret, "file:") {
		return fmt.Errorf("config: tls.dns01.rfc2136.secret must be an env: or file: reference, not an inline value")
	}
	if err := ValidateSecretReference("tls.dns01.rfc2136.secret", r.Secret); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	// The challenge of every certificate name lives at _acme-challenge.<name>:
	// the zone must contain it, or issuance would fail at the first renewal.
	for _, domain := range tls.Domains {
		home := challengeHome(tls, domain)
		if home != r.Zone && !strings.HasSuffix(home, "."+r.Zone) {
			return fmt.Errorf("config: tls.dns01.rfc2136.zone %q does not contain the challenge name for tls.domains entry %q (%s)", r.Zone, domain, home)
		}
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
	if source.GitHub != nil {
		github := *source.GitHub
		clone.GitHub = &github
	}
	clone.TLS.Domains = slices.Clone(source.TLS.Domains)
	if source.Tunnel != nil {
		tunnel := *source.Tunnel
		clone.Tunnel = &tunnel
	}
	if source.Pages != nil {
		pages := *source.Pages
		if source.Pages.Deploy != nil {
			deploy := *source.Pages.Deploy
			pages.Deploy = &deploy
		}
		clone.Pages = &pages
	}
	if source.Audit != nil {
		audit := *source.Audit
		audit.Include = slices.Clone(source.Audit.Include)
		clone.Audit = &audit
	}
	if source.Auth.Passkeys != nil {
		passkeys := *source.Auth.Passkeys
		clone.Auth.Passkeys = &passkeys
	}
	if source.TLS.DNS01 != nil {
		dns01 := *source.TLS.DNS01
		if source.TLS.DNS01.RFC2136 != nil {
			rfc2136 := *source.TLS.DNS01.RFC2136
			dns01.RFC2136 = &rfc2136
		}
		dns01.ChallengeDelegation = maps.Clone(source.TLS.DNS01.ChallengeDelegation)
		if source.TLS.DNS01.Builtin != nil {
			builtin := *source.TLS.DNS01.Builtin
			builtin.Nameservers = slices.Clone(builtin.Nameservers)
			dns01.Builtin = &builtin
		}
		if source.TLS.DNS01.Hetzner != nil {
			hetzner := *source.TLS.DNS01.Hetzner
			dns01.Hetzner = &hetzner
		}
		if source.TLS.DNS01.ExternalAccount != nil {
			ea := *source.TLS.DNS01.ExternalAccount
			dns01.ExternalAccount = &ea
		}
		clone.TLS.DNS01 = &dns01
	}
	if source.DNS != nil {
		dns := *source.DNS
		if source.DNS.Cloudflare != nil {
			cloudflare := *source.DNS.Cloudflare
			dns.Cloudflare = &cloudflare
		}
		clone.DNS = &dns
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
		clone.Routes[i] = CloneRoute(route)
	}
	clone.Projects = CloneProjects(source.Projects)
	return clone
}

// CloneProjects returns a deep copy of project configuration, including
// nested GitHub trust records.
func CloneProjects(projects []Project) []Project {
	clone := make([]Project, len(projects))
	for i, project := range projects {
		clone[i] = project
		clone[i].Hostnames = slices.Clone(project.Hostnames)
		clone[i].Deployers = slices.Clone(project.Deployers)
		for j, deployer := range project.Deployers {
			if deployer.GitHub != nil {
				github := *deployer.GitHub
				clone[i].Deployers[j].GitHub = &github
			}
		}
	}
	return clone
}

// CloneRoute returns a deep copy of one route.
func CloneRoute(route Route) Route {
	clone := route
	clone.Upstreams = slices.Clone(route.Upstreams)
	if route.Access != nil {
		access := *route.Access
		access.AllowedEmails = slices.Clone(route.Access.AllowedEmails)
		access.AllowedDomains = slices.Clone(route.Access.AllowedDomains)
		access.RequiredGroups = slices.Clone(route.Access.RequiredGroups)
		access.Methods = slices.Clone(route.Access.Methods)
		if route.Access.ContextRule != nil {
			rule := *route.Access.ContextRule
			rule.When.SessionDeviceBound = cloneBool(route.Access.ContextRule.When.SessionDeviceBound)
			rule.When.ActionRisky = cloneBool(route.Access.ContextRule.When.ActionRisky)
			rule.When.DeviceFirstSeen = cloneBool(route.Access.ContextRule.When.DeviceFirstSeen)
			access.ContextRule = &rule
		}
		clone.Access = &access
	}
	if route.Resource != nil {
		resource := *route.Resource
		resource.RiskyActions = slices.Clone(route.Resource.RiskyActions)
		clone.Resource = &resource
	}
	return clone
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// normalizeDNS checks the dns section: the target is an address or a plain
// hostname (no port, no wildcard), and a provider needs both a target to
// point records at and a token reference.
func normalizeDNS(dns *DNS) error {
	if dns == nil {
		return nil
	}
	if dns.Target != "" {
		if addr, err := netip.ParseAddr(dns.Target); err == nil {
			if addr.IsUnspecified() || addr.Zone() != "" {
				return fmt.Errorf("config: dns.target %q is not a usable public address", dns.Target)
			}
			dns.Target = addr.String()
		} else {
			host, err := normalizeHostname(dns.Target)
			if err != nil || strings.ContainsAny(host, ":*[") {
				return fmt.Errorf("config: dns.target must be an IP address or a hostname without port")
			}
			dns.Target = host
		}
	}
	if dns.Cloudflare != nil {
		if dns.Target == "" {
			return fmt.Errorf("config: dns.cloudflare requires dns.target, the address its records point at")
		}
		if strings.TrimSpace(dns.Cloudflare.Token) == "" {
			return fmt.Errorf("config: dns.cloudflare.token is required")
		}
		if err := ValidateSecretReference("dns.cloudflare.token", dns.Cloudflare.Token); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	return nil
}
