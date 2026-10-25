package config

import (
	"fmt"
	"net"
	"strings"
	"unicode"
)

// DeployEndpoint reserves a public hostname for project CI. Its listener
// settings require restart; project trust can be reloaded independently.
type DeployEndpoint struct {
	Host     string `json:"host"`
	Audience string `json:"audience"`
}

func validateDeployEndpoint(cfg *Config) error {
	if cfg.Pages == nil || cfg.Pages.Deploy == nil {
		return nil
	}
	deploy := cfg.Pages.Deploy
	host, err := NormalizeHost(deploy.Host)
	if err != nil || strings.ContainsAny(host, ":[]") || net.ParseIP(host) != nil {
		return fmt.Errorf("config: pages.deploy.host must be a valid dedicated hostname")
	}
	deploy.Host = host
	if deploy.Audience == "" || len(deploy.Audience) > 512 || strings.IndexFunc(deploy.Audience, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return fmt.Errorf("config: pages.deploy.audience must be a nonempty audience without whitespace or control characters")
	}
	for _, route := range cfg.Routes {
		if deployHostname(route.Host) == host {
			return fmt.Errorf("config: pages.deploy.host cannot be an application route")
		}
	}
	loginURLs := []string{cfg.Auth.RedirectURL}
	if cfg.Auth.Passkeys != nil {
		loginURLs = append(loginURLs, cfg.Auth.Passkeys.LoginURL)
	}
	for _, loginURL := range loginURLs {
		if loginURL == "" {
			continue
		}
		loginHost, err := hostOf(loginURL)
		if err == nil && deployHostname(loginHost) == host {
			return fmt.Errorf("config: pages.deploy.host cannot be a login hostname")
		}
	}
	if !cfg.TLS.Enabled {
		peer, _, err := net.SplitHostPort(cfg.Listen)
		ip := net.ParseIP(peer)
		if err != nil || (peer != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return fmt.Errorf("config: pages.deploy requires TLS on a public listener (plaintext is loopback-only)")
		}
	} else {
		name := host
		if h, _, err := net.SplitHostPort(host); err == nil {
			name = h
		}
		covered := false
		for _, domain := range cfg.TLS.Domains {
			domain = strings.ToLower(strings.TrimSuffix(domain, "."))
			if domain == name {
				covered = true
			}
			if strings.HasPrefix(domain, "*.") && strings.HasSuffix(name, domain[1:]) && strings.Count(name, ".") == strings.Count(domain, ".") {
				covered = true
			}
		}
		if !covered {
			return fmt.Errorf("config: tls.domains must cover pages.deploy.host")
		}
	}
	return nil
}

func deployHostname(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	return host
}
