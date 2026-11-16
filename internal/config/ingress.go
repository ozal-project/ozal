package config

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// Ingress modes: how visitors reach this edge.
const (
	// IngressDirect is the default: visitors (or an exterior proxy such as a
	// proxied DNS record) connect to this host's public address.
	IngressDirect = "direct"
	// IngressCloudflareTunnel publishes through a Cloudflare Tunnel: cloudflared
	// on this host dials out to Cloudflare and forwards to the loopback
	// listener, so the host needs no public address or open port.
	IngressCloudflareTunnel = "cloudflare_tunnel"
)

// DefaultCloudflareTunnelStateDir holds the tunnel identity Ozal created and
// the connector token cloudflared loads.
const DefaultCloudflareTunnelStateDir = "/var/lib/ozal/cloudflare-tunnel"

// Ingress selects how the public reaches this edge. Omitted means direct.
type Ingress struct {
	Mode             string                   `json:"mode"`
	CloudflareTunnel *CloudflareTunnelIngress `json:"cloudflare_tunnel,omitempty"`
}

// CloudflareTunnelIngress is the intent for a tunnel Ozal creates and owns
// through the Cloudflare API. Token is a secret reference (file: or env:) to
// an API token with Account · Cloudflare Tunnel · Edit plus DNS edit on the
// served zones; it is resolved only when the tunnel is inspected or changed.
type CloudflareTunnelIngress struct {
	Token    string `json:"token"`
	StateDir string `json:"state_dir,omitempty"`
	// Name is the tunnel's name in the Cloudflare account (default "ozal").
	Name string `json:"name,omitempty"`
}

var tunnelNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// IngressMode returns the configured mode, direct when omitted.
func (c Config) IngressMode() string {
	if c.Ingress == nil || c.Ingress.Mode == "" {
		return IngressDirect
	}
	return c.Ingress.Mode
}

// CloudflareTunnelTarget is the hostname a visitor-facing CNAME points at for
// a Cloudflare Tunnel; Cloudflare routes such a proxied record to the tunnel.
func CloudflareTunnelTarget(tunnelID string) string {
	return strings.ToLower(tunnelID) + ".cfargotunnel.com"
}

// normalizeIngress validates the mode and, for a Cloudflare Tunnel, the
// properties that make it safe: Ozal listens only on loopback (cloudflared on
// the same host is the only way in), serves plain HTTP (Cloudflare terminates
// TLS), trusts exactly the loopback peers for the visitor's IP and scheme,
// and runs its logins over HTTPS origins.
func normalizeIngress(cfg *Config) error {
	if cfg.Ingress == nil {
		return nil
	}
	in := cfg.Ingress
	if in.Mode == "" {
		in.Mode = IngressDirect
	}
	switch in.Mode {
	case IngressDirect:
		if in.CloudflareTunnel != nil {
			return fmt.Errorf("config: ingress.cloudflare_tunnel requires ingress.mode %q", IngressCloudflareTunnel)
		}
		return nil
	case IngressCloudflareTunnel:
	default:
		return fmt.Errorf("config: ingress.mode must be %q or %q", IngressDirect, IngressCloudflareTunnel)
	}

	tunnel := in.CloudflareTunnel
	if tunnel == nil {
		return fmt.Errorf("config: ingress.mode %q requires ingress.cloudflare_tunnel", IngressCloudflareTunnel)
	}
	if strings.TrimSpace(tunnel.Token) == "" {
		return fmt.Errorf("config: ingress.cloudflare_tunnel.token is required")
	}
	if !strings.HasPrefix(tunnel.Token, secretFilePrefix) && !strings.HasPrefix(tunnel.Token, secretEnvPrefix) {
		return fmt.Errorf("config: ingress.cloudflare_tunnel.token must be a file: or env: reference, never the token itself")
	}
	if err := ValidateSecretReference("ingress.cloudflare_tunnel.token", tunnel.Token); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if tunnel.StateDir == "" {
		tunnel.StateDir = DefaultCloudflareTunnelStateDir
	}
	if !filepath.IsAbs(tunnel.StateDir) || filepath.Clean(tunnel.StateDir) != tunnel.StateDir {
		return fmt.Errorf("config: ingress.cloudflare_tunnel.state_dir must be an absolute, clean path")
	}
	if tunnel.Name == "" {
		tunnel.Name = "ozal"
	}
	if !tunnelNamePattern.MatchString(tunnel.Name) {
		return fmt.Errorf("config: ingress.cloudflare_tunnel.name must be 1-63 letters, digits, '.', '_' or '-'")
	}

	if cfg.TLS.Enabled {
		return fmt.Errorf("config: ingress.mode %q requires tls.enabled false: Cloudflare terminates TLS and cloudflared reaches the loopback listener", IngressCloudflareTunnel)
	}
	if err := requireLoopbackListen(cfg.Listen); err != nil {
		return err
	}
	if cfg.DNS != nil && cfg.DNS.Target != "" {
		return fmt.Errorf("config: dns.target must be empty with ingress.mode %q: records point at the tunnel Ozal creates", IngressCloudflareTunnel)
	}
	if pk := cfg.Auth.Passkeys; pk != nil {
		if login, err := url.Parse(pk.LoginURL); err == nil && login.Scheme != "https" {
			return fmt.Errorf("config: auth.passkeys.login_url must be https with ingress.mode %q: visitors arrive over Cloudflare's HTTPS", IngressCloudflareTunnel)
		}
	}
	if cfg.Auth.RedirectURL != "" {
		if redirect, err := url.Parse(cfg.Auth.RedirectURL); err == nil && redirect.Scheme != "https" {
			return fmt.Errorf("config: auth.redirect_url must be https with ingress.mode %q", IngressCloudflareTunnel)
		}
	}

	// cloudflared connects from this host. Exactly the loopback peers may
	// assert the visitor's address and scheme; any other entry would let a
	// remote peer do the same.
	loopback := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	proxies, err := ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return err
	}
	for _, prefix := range proxies {
		if !prefix.Addr().IsLoopback() {
			return fmt.Errorf("config: trusted_proxies may list only loopback addresses with ingress.mode %q, got %s", IngressCloudflareTunnel, prefix)
		}
	}
	cfg.TrustedProxies = cfg.TrustedProxies[:0]
	for _, prefix := range loopback {
		cfg.TrustedProxies = append(cfg.TrustedProxies, prefix.String())
	}
	return nil
}

func requireLoopbackListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("config: listen must be a loopback IP:port with ingress.mode %q (for example 127.0.0.1:8888): only cloudflared on this host may connect", IngressCloudflareTunnel)
	}
	return nil
}

// CloudflareTunnelOrigin is the service cloudflared forwards every hostname
// to: this edge's loopback listener over plain HTTP.
func CloudflareTunnelOrigin(listen string) string {
	return "http://" + listen
}
