package config

import (
	"errors"
	"net/url"
	"strings"
)

// ParseRedirect checks a redirect route's destination: an absolute http(s)
// URL with a host and nothing a visitor's path could change the meaning of
// (no user info, query or fragment). The path, when present, is a prefix the
// request path is appended to. A redirect to the route's own host would loop
// and is refused.
func ParseRedirect(routeHost, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("redirect must be an absolute http(s) URL such as https://example.com")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("redirect may not carry user info, a query or a fragment")
	}
	if _, err := NormalizeHost(u.Host); err != nil {
		return nil, errors.New("redirect has an invalid host")
	}
	if strings.EqualFold(routeHostname(routeHost), u.Hostname()) {
		return nil, errors.New("redirect points at the route's own host")
	}
	return u, nil
}
