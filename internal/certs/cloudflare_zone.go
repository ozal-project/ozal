package certs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Zone setup (EDGE-009): the proxy settings a domain needs before its
// addresses work through this edge. Each call reads or changes exactly one
// setting of one zone; none deletes or replaces something the operator made.

// ZoneClientCertificate is a zone-level Authenticated Origin Pulls
// certificate as Cloudflare reports it. Certificate is the public PEM only:
// the API never returns the private key.
type ZoneClientCertificate struct {
	ID          string `json:"id"`
	Certificate string `json:"certificate"`
	Status      string `json:"status"`
	UploadedOn  string `json:"uploaded_on"`
}

// ErrNotEmpty means a name already holds a record; zone setup never replaces one.
var ErrNotEmpty = errors.New("cloudflare: the name already has a record")

func (c *CloudflareClient) zonePath(ctx context.Context, zone, suffix string) (string, error) {
	id, err := c.zoneFor(ctx, zone)
	if err != nil {
		return "", err
	}
	return "/zones/" + url.PathEscape(id) + suffix, nil
}

// ZoneSSLMode reads the proxy's encryption mode: off, flexible, full or strict.
func (c *CloudflareClient) ZoneSSLMode(ctx context.Context, zone string) (string, error) {
	path, err := c.zonePath(ctx, zone, "/settings/ssl")
	if err != nil {
		return "", err
	}
	var setting struct {
		Value string `json:"value"`
	}
	if err := c.call(ctx, http.MethodGet, path, nil, &setting); err != nil {
		return "", err
	}
	return setting.Value, nil
}

// SetZoneSSLStrict switches the zone to Full (strict).
func (c *CloudflareClient) SetZoneSSLStrict(ctx context.Context, zone string) error {
	path, err := c.zonePath(ctx, zone, "/settings/ssl")
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPatch, path, map[string]string{"value": "strict"}, nil)
}

// ZoneOriginPull reads the zone-level setting and its uploaded certificates.
func (c *CloudflareClient) ZoneOriginPull(ctx context.Context, zone string) (enabled bool, certificates []ZoneClientCertificate, err error) {
	settings, err := c.zonePath(ctx, zone, "/origin_tls_client_auth/settings")
	if err != nil {
		return false, nil, err
	}
	var setting struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.call(ctx, http.MethodGet, settings, nil, &setting); err != nil {
		return false, nil, err
	}
	list, err := c.zonePath(ctx, zone, "/origin_tls_client_auth")
	if err != nil {
		return false, nil, err
	}
	if err := c.call(ctx, http.MethodGet, list, nil, &certificates); err != nil {
		return false, nil, err
	}
	return setting.Enabled, certificates, nil
}

// UploadZoneClientCertificate adds a zone-level client certificate. Existing
// certificates stay; the key goes only into this request body, which no
// error message repeats.
func (c *CloudflareClient) UploadZoneClientCertificate(ctx context.Context, zone string, certPEM, keyPEM []byte) (ZoneClientCertificate, error) {
	path, err := c.zonePath(ctx, zone, "/origin_tls_client_auth")
	if err != nil {
		return ZoneClientCertificate{}, err
	}
	var created ZoneClientCertificate
	payload := map[string]string{"certificate": string(certPEM), "private_key": string(keyPEM)}
	if err := c.call(ctx, http.MethodPost, path, payload, &created); err != nil {
		return ZoneClientCertificate{}, err
	}
	if created.ID == "" {
		return ZoneClientCertificate{}, fmt.Errorf("%w: the API returned no certificate ID", ErrAmbiguous)
	}
	return created, nil
}

// EnableZoneOriginPull turns zone-level Authenticated Origin Pulls on.
func (c *CloudflareClient) EnableZoneOriginPull(ctx context.Context, zone string) error {
	path, err := c.zonePath(ctx, zone, "/origin_tls_client_auth/settings")
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPut, path, map[string]bool{"enabled": true}, nil)
}

// NameRecord describes what is at a name: the CNAME target, or the type of
// another record found there.
type NameRecord struct {
	Type    string
	Content string
}

// RecordsAt lists every record at exactly name.
func (c *CloudflareClient) RecordsAt(ctx context.Context, name string) ([]NameRecord, error) {
	path, err := c.zonePath(ctx, name, "/dns_records?"+url.Values{"name": {name}}.Encode())
	if err != nil {
		return nil, err
	}
	var records []struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := c.call(ctx, http.MethodGet, path, nil, &records); err != nil {
		return nil, err
	}
	var out []NameRecord
	for _, r := range records {
		if strings.EqualFold(r.Name, name) {
			out = append(out, NameRecord{Type: r.Type, Content: strings.TrimSuffix(strings.ToLower(r.Content), ".")})
		}
	}
	return out, nil
}

// CreateChallengeCNAME creates name -> target, unproxied, only when nothing
// is at name yet.
func (c *CloudflareClient) CreateChallengeCNAME(ctx context.Context, name, target string) error {
	existing, err := c.RecordsAt(ctx, name)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return ErrNotEmpty
	}
	path, err := c.zonePath(ctx, name, "/dns_records")
	if err != nil {
		return err
	}
	payload := map[string]any{"type": "CNAME", "name": name, "content": target, "proxied": false, "ttl": 1}
	var created struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, path, payload, &created); err != nil {
		return err
	}
	if created.ID == "" {
		return fmt.Errorf("%w: the API returned no record ID for %s", ErrAmbiguous, name)
	}
	return nil
}
