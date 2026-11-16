package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// AdminRemote serves the console on its own HTTPS host through the public
// listener. The admin listener itself stays on loopback and the socket; this
// host answers only the console's sign-in until an administrator passkey
// session opened there is presented. Removing the block (and reloading) is
// the kill switch: the host stops answering and its sessions end.
type AdminRemote struct {
	// Host is the dedicated hostname, e.g. "console.example.com". It is not
	// an application route, the login host or the deploy host.
	Host string `json:"host"`
}

// RemoteConsoleHost is the normalized remote console host, or "" when the
// remote console is off.
func (c Config) RemoteConsoleHost() string {
	if c.Admin.Remote == nil {
		return ""
	}
	return c.Admin.Remote.Host
}

// validateAdminRemote normalizes admin.remote and refuses every shape that
// would weaken it: plain HTTP to the visitor, a sign-in that is not an
// administrator passkey on the HTTPS login host, or a host shared with
// something else the edge serves.
func validateAdminRemote(cfg *Config) error {
	remote := cfg.Admin.Remote
	if remote == nil {
		return nil
	}
	host, err := NormalizeHost(remote.Host)
	if err != nil || strings.ContainsAny(host, ":[]") || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return fmt.Errorf("config: admin.remote.host must be a dedicated hostname such as console.example.com")
	}
	remote.Host = host
	if !cfg.Admin.LoginHost || cfg.Auth.Passkeys == nil {
		return fmt.Errorf("config: admin.remote needs admin.login_host and auth.passkeys: the remote console signs in only with an administrator passkey on the HTTPS login host")
	}
	login, err := url.Parse(cfg.Auth.Passkeys.LoginURL)
	if err != nil || login.Scheme != "https" || login.Host == "" {
		return fmt.Errorf("config: admin.remote needs an https auth.passkeys.login_url")
	}
	if !cfg.TLS.Enabled && cfg.IngressMode() != IngressCloudflareTunnel {
		return fmt.Errorf("config: admin.remote needs HTTPS to the visitor: tls.enabled, or ingress.mode %q", IngressCloudflareTunnel)
	}
	if deployHostname(strings.ToLower(login.Host)) == host {
		return fmt.Errorf("config: admin.remote.host cannot be the login host; give the console its own hostname")
	}
	for _, route := range cfg.Routes {
		if deployHostname(route.Host) == host {
			return fmt.Errorf("config: admin.remote.host cannot be an application route (%s)", route.Host)
		}
	}
	if cfg.Pages != nil && cfg.Pages.Deploy != nil && deployHostname(cfg.Pages.Deploy.Host) == host {
		return fmt.Errorf("config: admin.remote.host cannot be pages.deploy.host")
	}
	return nil
}
