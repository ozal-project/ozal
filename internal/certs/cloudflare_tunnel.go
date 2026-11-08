package certs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CloudflareTunnel is a tunnel as the account reports it. It carries no
// secret: the connector token has its own call.
type CloudflareTunnel struct {
	ID          string
	Name        string
	Remote      bool
	Status      string
	Connections int
	Deleted     bool
}

type cloudflareTunnelBody struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	RemoteConfig bool       `json:"remote_config"`
	Status       string     `json:"status"`
	DeletedAt    *time.Time `json:"deleted_at"`
	Connections  []struct {
		ID string `json:"id"`
	} `json:"connections"`
}

func (b cloudflareTunnelBody) tunnel() CloudflareTunnel {
	return CloudflareTunnel{ID: b.ID, Name: b.Name, Remote: b.RemoteConfig, Status: b.Status, Connections: len(b.Connections), Deleted: b.DeletedAt != nil}
}

// TunnelIngressRule is one rule of a remotely managed tunnel's ingress.
type TunnelIngressRule struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
}

// ErrTunnelMissing means the account has no such tunnel (never created, or
// deleted in the dashboard).
var ErrTunnelMissing = errors.New("cloudflare: the tunnel does not exist")

// AccountForZone returns the account that owns the zone covering name. The
// tunnel is created in that account, so its records can route to it.
func (c *CloudflareClient) AccountForZone(ctx context.Context, name string) (string, error) {
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(name), "."), ".")
	for i := 0; i+2 <= len(labels); i++ {
		candidate := strings.Join(labels[i:], ".")
		var zones []struct {
			Name    string `json:"name"`
			Account struct {
				ID string `json:"id"`
			} `json:"account"`
		}
		if err := c.call(ctx, http.MethodGet, "/zones?"+url.Values{"name": {candidate}}.Encode(), nil, &zones); err != nil {
			return "", err
		}
		if len(zones) > 0 && strings.EqualFold(zones[0].Name, candidate) && zones[0].Account.ID != "" {
			return zones[0].Account.ID, nil
		}
	}
	return "", fmt.Errorf("cloudflare: no zone in this token's reach covers %q", name)
}

func tunnelPath(account string, rest ...string) string {
	path := "/accounts/" + url.PathEscape(account) + "/cfd_tunnel"
	for _, part := range rest {
		path += "/" + url.PathEscape(part)
	}
	return path
}

// FindTunnel returns the active tunnel with exactly this name, if any. It is
// how a lost local state, or a create whose answer was lost, is recovered
// without making a second tunnel.
func (c *CloudflareClient) FindTunnel(ctx context.Context, account, name string) (CloudflareTunnel, bool, error) {
	var tunnels []cloudflareTunnelBody
	query := url.Values{"name": {name}, "is_deleted": {"false"}}.Encode()
	if err := c.call(ctx, http.MethodGet, tunnelPath(account)+"?"+query, nil, &tunnels); err != nil {
		return CloudflareTunnel{}, false, err
	}
	for _, t := range tunnels {
		if t.Name == name && t.DeletedAt == nil {
			return t.tunnel(), true, nil
		}
	}
	return CloudflareTunnel{}, false, nil
}

// CreateTunnel creates a remotely managed tunnel and returns it with its
// connector token. A lost answer is ErrAmbiguous: call FindTunnel before
// trying again.
func (c *CloudflareClient) CreateTunnel(ctx context.Context, account, name string) (CloudflareTunnel, string, error) {
	var created struct {
		cloudflareTunnelBody
		Token string `json:"token"`
	}
	if err := c.call(ctx, http.MethodPost, tunnelPath(account), map[string]any{"name": name, "config_src": "cloudflare"}, &created); err != nil {
		return CloudflareTunnel{}, "", err
	}
	if created.ID == "" {
		return CloudflareTunnel{}, "", fmt.Errorf("%w: the API returned no tunnel ID", ErrAmbiguous)
	}
	return created.tunnel(), created.Token, nil
}

// TunnelToken fetches the connector token of an existing tunnel.
func (c *CloudflareClient) TunnelToken(ctx context.Context, account, id string) (string, error) {
	var token string
	if err := c.call(ctx, http.MethodGet, tunnelPath(account, id, "token"), nil, &token); err != nil {
		if errors.Is(err, errRecordMissing) {
			return "", ErrTunnelMissing
		}
		return "", err
	}
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("%w: the API returned an empty tunnel token", ErrAmbiguous)
	}
	return token, nil
}

// Tunnel reads a tunnel's status and connections.
func (c *CloudflareClient) Tunnel(ctx context.Context, account, id string) (CloudflareTunnel, error) {
	var body cloudflareTunnelBody
	if err := c.call(ctx, http.MethodGet, tunnelPath(account, id), nil, &body); err != nil {
		if errors.Is(err, errRecordMissing) {
			return CloudflareTunnel{}, ErrTunnelMissing
		}
		return CloudflareTunnel{}, err
	}
	if body.DeletedAt != nil {
		return CloudflareTunnel{}, ErrTunnelMissing
	}
	return body.tunnel(), nil
}

// TunnelIngress reads a remotely managed tunnel's ingress rules.
func (c *CloudflareClient) TunnelIngress(ctx context.Context, account, id string) ([]TunnelIngressRule, error) {
	var body struct {
		Config *struct {
			Ingress []TunnelIngressRule `json:"ingress"`
		} `json:"config"`
	}
	if err := c.call(ctx, http.MethodGet, tunnelPath(account, id, "configurations"), nil, &body); err != nil {
		if errors.Is(err, errRecordMissing) {
			return nil, ErrTunnelMissing
		}
		return nil, err
	}
	if body.Config == nil {
		return nil, nil
	}
	return body.Config.Ingress, nil
}

// SetTunnelIngress replaces a remotely managed tunnel's ingress rules.
func (c *CloudflareClient) SetTunnelIngress(ctx context.Context, account, id string, rules []TunnelIngressRule) error {
	if len(rules) == 0 || rules[len(rules)-1].Hostname != "" {
		return errors.New("cloudflare: a tunnel ingress must end with a catch-all rule")
	}
	return c.call(ctx, http.MethodPut, tunnelPath(account, id, "configurations"), map[string]any{"config": map[string]any{"ingress": rules}}, nil)
}
