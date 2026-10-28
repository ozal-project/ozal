package certs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultCloudflareBase = "https://api.cloudflare.com/client/v4"
	maxCloudflareBody     = 1 << 20
)

// CloudflareConfig configures the DNS adapter. Token is the resolved API
// token — zone-scoped, least privilege — and never appears in logs; BaseURL
// and HTTPClient exist so tests point the adapter at a simulated API.
type CloudflareConfig struct {
	Token      string
	BaseURL    string
	HTTPClient *http.Client
}

// CloudflareClient creates and deletes DNS-01 TXT records through the
// Cloudflare API with net/http. It implements DNSProvider: every create
// returns the exact record ID, and only that ID can be deleted — there is
// deliberately no "delete everything at this name" call.
type CloudflareClient struct {
	token string
	base  string
	http  *http.Client

	mu    sync.Mutex
	zones map[string]string // record name or zone name -> zone ID
}

// NewCloudflareClient validates the token and applies defaults. An empty
// token fails here, at composition time, not on the first challenge.
func NewCloudflareClient(cfg CloudflareConfig) (*CloudflareClient, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("certs: a Cloudflare API token is required for dns-01")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultCloudflareBase
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &CloudflareClient{
		token: cfg.Token,
		base:  strings.TrimSuffix(cfg.BaseURL, "/"),
		http:  cfg.HTTPClient,
		zones: map[string]string{},
	}, nil
}

// CreateTXT creates the challenge record and returns its exact identity. A
// transport error or a 5xx reply is reported as ErrAmbiguous: the record may
// exist even though the response was lost.
func (c *CloudflareClient) CreateTXT(ctx context.Context, name, value string) (DNSRecord, error) {
	zoneID, err := c.zoneFor(ctx, name)
	if err != nil {
		return DNSRecord{}, err
	}
	payload := map[string]any{"type": "TXT", "name": name, "content": value, "ttl": 60}
	var result struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", payload, &result); err != nil {
		return DNSRecord{}, err
	}
	if result.ID == "" {
		return DNSRecord{}, fmt.Errorf("%w: the API returned no record ID for %s", ErrAmbiguous, name)
	}
	return DNSRecord{ZoneID: zoneID, ID: result.ID, Name: name, Value: value}, nil
}

// DeleteTXT removes one record by ID. A record that is already gone counts
// as deleted.
func (c *CloudflareClient) DeleteTXT(ctx context.Context, record DNSRecord) error {
	if record.ZoneID == "" || record.ID == "" {
		return fmt.Errorf("%w: refusing to delete an unidentified record", ErrDNS)
	}
	err := c.call(ctx, http.MethodDelete, "/zones/"+url.PathEscape(record.ZoneID)+"/dns_records/"+url.PathEscape(record.ID), nil, nil)
	if errors.Is(err, errRecordMissing) {
		return nil
	}
	return err
}

var errRecordMissing = errors.New("record is gone")

type cloudflareEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func (c *CloudflareClient) call(ctx context.Context, method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("certs: encode cloudflare payload: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %v", ErrAmbiguous, method, path, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxCloudflareBody))
	if err != nil {
		return fmt.Errorf("%w: %s %s: read response: %v", ErrAmbiguous, method, path, err)
	}
	if res.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s %s", errRecordMissing, method, path)
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return &RateLimitedError{RetryAfter: retryAfterSeconds(res.Header.Get("Retry-After")), Err: fmt.Errorf("cloudflare: %s %s: rate limited", method, path)}
	}
	var envelope cloudflareEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("%w: %s %s: unexpected response (status %d)", ErrAmbiguous, method, path, res.StatusCode)
	}
	if res.StatusCode >= 500 {
		// A 5xx may have applied the change; never treat it as a clean
		// failure on a create.
		return fmt.Errorf("%w: %s %s: server error %d: %s", ErrAmbiguous, method, path, res.StatusCode, firstCloudflareMessage(envelope))
	}
	if !envelope.Success {
		return fmt.Errorf("cloudflare: %s %s: API error %d: %s", method, path, res.StatusCode, firstCloudflareMessage(envelope))
	}
	if out != nil {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("%w: %s %s: decode result: %v", ErrAmbiguous, method, path, err)
		}
	}
	return nil
}

func firstCloudflareMessage(envelope cloudflareEnvelope) string {
	if len(envelope.Errors) == 0 {
		return "no detail"
	}
	return fmt.Sprintf("code %d: %s", envelope.Errors[0].Code, envelope.Errors[0].Message)
}

func retryAfterSeconds(value string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 0
}

// zoneFor finds the zone that owns name by asking the API for progressively
// shorter suffixes. The answer is cached, so one issuance costs one lookup.
func (c *CloudflareClient) zoneFor(ctx context.Context, name string) (string, error) {
	c.mu.Lock()
	cached, ok := c.zones[name]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}

	labels := strings.Split(name, ".")
	for i := 0; i+2 <= len(labels); i++ {
		candidate := strings.Join(labels[i:], ".")
		var zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		query := url.Values{"name": {candidate}}.Encode()
		if err := c.call(ctx, http.MethodGet, "/zones?"+query, nil, &zones); err != nil {
			return "", err
		}
		if len(zones) > 0 && strings.EqualFold(zones[0].Name, candidate) {
			c.mu.Lock()
			c.zones[name] = zones[0].ID
			c.zones[candidate] = zones[0].ID
			c.mu.Unlock()
			return zones[0].ID, nil
		}
	}
	return "", fmt.Errorf("%w: no Cloudflare zone covers %q", ErrDNS, name)
}

// SiteRecord is a visitor-facing address record (A, AAAA or CNAME) that the
// console creates for a new hostname (UI-006).
type SiteRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

// ErrRecordExists means the name already has an address record. The console
// never replaces one: it may be the live hosting the operator is moving from.
var ErrRecordExists = errors.New("cloudflare: the hostname already has an address record")

// SiteRecords lists the A, AAAA and CNAME records at exactly name.
func (c *CloudflareClient) SiteRecords(ctx context.Context, name string) ([]SiteRecord, error) {
	zoneID, err := c.zoneFor(ctx, name)
	if err != nil {
		return nil, err
	}
	var records []SiteRecord
	query := url.Values{"name": {name}}.Encode()
	if err := c.call(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/dns_records?"+query, nil, &records); err != nil {
		return nil, err
	}
	out := records[:0]
	for _, r := range records {
		if (r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME") && strings.EqualFold(r.Name, name) {
			out = append(out, r)
		}
	}
	return out, nil
}

// CreateSiteRecord adds record only when its name has no address record yet.
// The check and the create are two calls, so a record created in between by
// someone else is reported by Cloudflare as a conflict rather than replaced.
func (c *CloudflareClient) CreateSiteRecord(ctx context.Context, record SiteRecord) (SiteRecord, error) {
	if record.Type != "A" && record.Type != "AAAA" && record.Type != "CNAME" {
		return SiteRecord{}, fmt.Errorf("%w: refusing record type %q", ErrDNS, record.Type)
	}
	existing, err := c.SiteRecords(ctx, record.Name)
	if err != nil {
		return SiteRecord{}, err
	}
	if len(existing) > 0 {
		return SiteRecord{}, ErrRecordExists
	}
	zoneID, err := c.zoneFor(ctx, record.Name)
	if err != nil {
		return SiteRecord{}, err
	}
	payload := map[string]any{"type": record.Type, "name": record.Name, "content": record.Content, "proxied": record.Proxied, "ttl": 1}
	var created SiteRecord
	if err := c.call(ctx, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", payload, &created); err != nil {
		return SiteRecord{}, err
	}
	if created.ID == "" {
		return SiteRecord{}, fmt.Errorf("%w: the API returned no record ID for %s", ErrAmbiguous, record.Name)
	}
	return created, nil
}
