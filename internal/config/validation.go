package config

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// NormalizeHost returns the canonical routing key for a host or host:port.
// DNS names are case-insensitive and may arrive with a trailing root dot.
func NormalizeHost(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("host is empty")
	}
	if strings.TrimSpace(raw) != raw {
		return "", fmt.Errorf("host %q contains surrounding whitespace", raw)
	}

	hasPort := (strings.HasPrefix(raw, "[") && !strings.HasSuffix(raw, "]")) ||
		strings.Count(raw, ":") == 1
	host, port, err := splitHostPort(raw)
	if err != nil {
		return "", err
	}
	host, err = normalizeHostname(host)
	if hasPort && port == "" {
		return "", fmt.Errorf("host %q has an empty port", raw)
	}
	if err != nil {
		return "", err
	}

	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("host %q has invalid port %q", raw, port)
		}
		return net.JoinHostPort(host, port), nil
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]", nil
	}
	return host, nil
}

func splitHostPort(raw string) (host, port string, err error) {
	if strings.HasPrefix(raw, "[") {
		if strings.HasSuffix(raw, "]") {
			host = strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
			if net.ParseIP(host) == nil {
				return "", "", fmt.Errorf("host %q has brackets around a non-IP address", raw)
			}
			return host, "", nil
		}
		host, port, err = net.SplitHostPort(raw)
		if err != nil {
			return "", "", fmt.Errorf("invalid host %q: %w", raw, err)
		}
		return host, port, nil
	}

	switch strings.Count(raw, ":") {
	case 0:
		return raw, "", nil
	case 1:
		host, port, err = net.SplitHostPort(raw)
		if err != nil {
			return "", "", fmt.Errorf("invalid host %q: %w", raw, err)
		}
		return host, port, nil
	default:
		if net.ParseIP(raw) == nil {
			return "", "", fmt.Errorf("invalid unbracketed IPv6 host %q", raw)
		}
		return raw, "", nil
	}
}

func normalizeHostname(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSuffix(raw, "."))
	if host == "" {
		return "", fmt.Errorf("hostname is empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	if len(host) > 253 {
		return "", fmt.Errorf("hostname %q is too long", raw)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return "", fmt.Errorf("hostname %q has an invalid label", raw)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("hostname %q has a label starting or ending with '-'", raw)
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return "", fmt.Errorf("hostname %q contains invalid character %q", raw, ch)
			}
		}
	}
	return host, nil
}

// ParseTarget validates and parses an upstream URL.
func ParseTarget(raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	target.Scheme = strings.ToLower(target.Scheme)
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("scheme must be http or https")
	}
	if target.Hostname() == "" {
		return nil, fmt.Errorf("URL has no host")
	}
	return target, nil
}

func normalizeCookieDomain(raw string) (string, error) {
	domain, err := normalizeHostname(strings.TrimPrefix(raw, "."))
	if err != nil {
		return "", fmt.Errorf("invalid cookie_domain %q: %w", raw, err)
	}
	if net.ParseIP(domain) != nil {
		return "", fmt.Errorf("cookie_domain %q must be a DNS name", raw)
	}
	return domain, nil
}

func cookieDomainCovers(host, domain string) bool {
	hostname := hostnameOnly(host)
	return hostname == domain || strings.HasSuffix(hostname, "."+domain)
}

func hostnameOnly(host string) string {
	if strings.HasPrefix(host, "[") {
		if end := strings.IndexByte(host, ']'); end >= 0 {
			return host[1:end]
		}
	}
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		return hostname
	}
	return host
}

func validateAdminListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("admin.listen must be an IP host:port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("admin.listen must use a loopback IP address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("admin.listen has invalid port %q", port)
	}
	return nil
}

// maxSocketPath fits sockaddr_un.sun_path with its terminating NUL on every
// unix Switchboard builds for: 104 bytes on macOS and the BSDs (108 on
// Linux), so a config valid on one host stays valid on another.
const maxSocketPath = 103

// validateAdminSocket accepts an empty value (no socket) or an absolute,
// clean path short enough for a unix socket address.
func validateAdminSocket(path string) error {
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("admin.socket must be an absolute, clean path")
	}
	if len(path) > maxSocketPath {
		return fmt.Errorf("admin.socket is longer than %d bytes", maxSocketPath)
	}
	return nil
}
