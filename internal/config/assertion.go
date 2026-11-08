package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
)

// IdentityAssertion configures the key this edge signs identity assertions
// with. KeyDir holds the Ed25519 key, created on first start (0700/0600).
type IdentityAssertion struct {
	KeyDir string `json:"key_dir"`
}

var audiencePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// AssertionIssuer is the issuer an application expects: the origin of the
// passkey login_url or, with external OIDC only, of the OIDC redirect_url.
// It names this edge's sign-in, which is stable across routes.
func (c Config) AssertionIssuer() string {
	for _, raw := range []string{passkeyLoginURL(c), c.Auth.RedirectURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return ""
}

func passkeyLoginURL(c Config) string {
	if c.Auth.Passkeys == nil {
		return ""
	}
	return c.Auth.Passkeys.LoginURL
}

// validateRouteAssertion allows an assertion only where it means something:
// a protected route (there is a person to assert) that forwards to an
// application this edge reaches directly. A connector route would carry it
// through the tunnel, where the connector strips it as client input.
func validateRouteAssertion(cfg *Config, r *Route) error {
	if r.IdentityAssertion == nil {
		return nil
	}
	if !audiencePattern.MatchString(r.IdentityAssertion.Audience) {
		return fmt.Errorf("config: route %q identity_assertion.audience must be 1-64 characters of a-z, 0-9, '.', '_' or '-'", r.Host)
	}
	if !r.RequiresAuth() {
		return fmt.Errorf("config: route %q identity_assertion needs access: a public route has no person to assert", r.Host)
	}
	if r.Target == "" && r.Upstreams == nil {
		return fmt.Errorf("config: route %q identity_assertion needs target or upstreams: only applications receive it", r.Host)
	}
	if cfg.IdentityAssertion == nil {
		return fmt.Errorf("config: route %q identity_assertion needs top-level identity_assertion.key_dir", r.Host)
	}
	dir := cfg.IdentityAssertion.KeyDir
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("config: identity_assertion.key_dir must be an absolute, clean path")
	}
	if cfg.AssertionIssuer() == "" {
		return fmt.Errorf("config: route %q identity_assertion needs a login (auth.passkeys.login_url or auth.redirect_url) to name the issuer", r.Host)
	}
	return nil
}
