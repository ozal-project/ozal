package certs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultHetznerBase = "https://api.hetzner.cloud/v1"
	maxHetznerBody     = 1 << 20
	hetznerTTL         = 60 // the API minimum: a stale TXT must age out quickly
)

// HetznerConfig configures the Hetzner DNS adapter. Token is the resolved
// API token and never appears in logs; Zone is explicit, so the adapter
// needs no zone lookup and the token no permission to list zones. BaseURL,
// HTTPClient and the poll settings exist so tests use a simulated API.
type HetznerConfig struct {
	Token         string
	Zone          string
	BaseURL       string
	HTTPClient    *http.Client
	PollInterval  time.Duration
	ActionTimeout time.Duration
}

// HetznerClient creates and deletes DNS-01 TXT records through the Hetzner
// Cloud DNS API (RRSets, /v1/zones/{zone}/rrsets/{name}/{type}/actions/...).
// The API identifies a record by its value inside an RRset, so a delete
// removes exactly the record this client created and never the set. Every
// change is an asynchronous Action: the adapter waits for it to finish
// before reporting the record as created or deleted.
type HetznerClient struct {
	token   string
	zone    string // lower-case, no trailing dot
	base    string
	http    *http.Client
	poll    time.Duration
	timeout time.Duration
}

// NewHetznerClient validates the configuration at composition time.
func NewHetznerClient(cfg HetznerConfig) (*HetznerClient, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("certs: a Hetzner API token is required for dns-01")
	}
	zone := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.Zone)), ".")
	if zone == "" || strings.Contains(zone, "..") || strings.ContainsAny(zone, "/?# ") {
		return nil, errors.New("certs: hetzner requires the zone that holds the challenge records")
	}
	if _, err := wireName(zone); err != nil {
		return nil, errors.New("certs: hetzner zone is not a valid domain name")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultHetznerBase
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.ActionTimeout <= 0 {
		cfg.ActionTimeout = 30 * time.Second
	}
	client := *cfg.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HetznerClient{
		token: cfg.Token, zone: zone, base: strings.TrimSuffix(cfg.BaseURL, "/"),
		http: &client, poll: cfg.PollInterval, timeout: cfg.ActionTimeout,
	}, nil
}

// hetznerTXTValue is the one place that decides how a TXT value is written
// in a request. The API reference does not state the quoting rule; a
// secondary source quotes the value, so the value is sent quoted until a
// live test against a real zone says otherwise. Add and remove both go
// through it, so they always agree.
func hetznerTXTValue(value string) (string, error) {
	if value == "" || len(value) > maxTXTString {
		return "", fmt.Errorf("%w: txt value must be 1 to %d bytes", ErrDNS, maxTXTString)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return "", fmt.Errorf("%w: txt value has a character the quoting cannot carry", ErrDNS)
		}
	}
	return `"` + value + `"`, nil
}

func (c *HetznerClient) relative(name string) (string, bool) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	switch {
	case name == c.zone:
		return "@", true
	case strings.HasSuffix(name, "."+c.zone):
		return strings.TrimSuffix(name, "."+c.zone), true
	}
	return "", false
}

func (c *HetznerClient) recordID(name, value string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + value))
	return "hetzner:" + hex.EncodeToString(sum[:8])
}

// CreateTXT adds the challenge record and waits for the change to apply. A
// transport error, a 5xx or an Action that never finishes is ErrAmbiguous:
// the record may exist.
func (c *HetznerClient) CreateTXT(ctx context.Context, name, value string) (DNSRecord, error) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	rel, ok := c.relative(name)
	if !ok {
		return DNSRecord{}, fmt.Errorf("%w: %s is outside the configured zone %s", ErrDNS, name, c.zone)
	}
	quoted, err := hetznerTXTValue(value)
	if err != nil {
		return DNSRecord{}, err
	}
	body := map[string]any{"ttl": hetznerTTL, "records": []map[string]string{{"value": quoted}}}
	if err := c.change(ctx, rel, "add_records", body); err != nil {
		if errors.Is(err, errRecordMissing) {
			return DNSRecord{}, fmt.Errorf("%w: hetzner zone %s was not found for this token", ErrDNS, c.zone)
		}
		return DNSRecord{}, err
	}
	return DNSRecord{ZoneID: c.zone, ID: c.recordID(name, value), Name: name, Value: value}, nil
}

// DeleteTXT removes exactly the record CreateTXT returned. A record or set
// that is already gone counts as deleted.
func (c *HetznerClient) DeleteTXT(ctx context.Context, record DNSRecord) error {
	name := strings.TrimSuffix(strings.ToLower(record.Name), ".")
	rel, ok := c.relative(name)
	if record.ZoneID != c.zone || record.ID == "" || record.Value == "" || !ok || record.ID != c.recordID(name, record.Value) {
		return fmt.Errorf("%w: refusing to delete an unidentified record", ErrDNS)
	}
	quoted, err := hetznerTXTValue(record.Value)
	if err != nil {
		return err
	}
	body := map[string]any{"records": []map[string]string{{"value": quoted}}}
	err = c.change(ctx, rel, "remove_records", body)
	if errors.Is(err, errRecordMissing) {
		return nil
	}
	return err
}

func (c *HetznerClient) change(ctx context.Context, rel, action string, body any) error {
	path := "/zones/" + url.PathEscape(c.zone) + "/rrsets/" + url.PathEscape(rel) + "/TXT/actions/" + action
	var out struct {
		Action struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"action"`
	}
	if err := c.call(ctx, http.MethodPost, path, body, &out); err != nil {
		return err
	}
	if out.Action.ID == 0 {
		return fmt.Errorf("%w: hetzner %s returned no action", ErrAmbiguous, action)
	}
	return c.await(ctx, out.Action.ID, out.Action.Status, action)
}

// await polls an Action until it succeeds or fails. The API reference says
// to wait for completion "to ensure your changes are applied".
func (c *HetznerClient) await(ctx context.Context, id int64, status, action string) error {
	deadline := time.Now().Add(c.timeout)
	for {
		switch status {
		case "success":
			return nil
		case "error":
			return fmt.Errorf("%w: hetzner %s failed", ErrDNS, action)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: hetzner %s did not finish within %s", ErrAmbiguous, action, c.timeout)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: hetzner %s: %v", ErrAmbiguous, action, ctx.Err())
		case <-time.After(c.poll):
		}
		var out struct {
			Action struct {
				Status string `json:"status"`
			} `json:"action"`
		}
		if err := c.call(ctx, http.MethodGet, fmt.Sprintf("/zones/actions/%d", id), nil, &out); err != nil {
			if errors.Is(err, errRecordMissing) {
				return fmt.Errorf("%w: hetzner %s: the action disappeared", ErrAmbiguous, action)
			}
			return err
		}
		status = out.Action.Status
	}
}

type hetznerError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *HetznerClient) call(ctx context.Context, method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("certs: encode hetzner payload: %w", err)
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
	data, err := io.ReadAll(io.LimitReader(res.Body, maxHetznerBody))
	if err != nil {
		return fmt.Errorf("%w: %s %s: read response: %v", ErrAmbiguous, method, path, err)
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s %s", errRecordMissing, method, path)
	case res.StatusCode == http.StatusTooManyRequests:
		return &RateLimitedError{RetryAfter: retryAfterSeconds(res.Header.Get("Retry-After")), Err: fmt.Errorf("hetzner: %s %s: rate limited", method, path)}
	case res.StatusCode >= 500:
		// A 5xx may have applied the change; never a clean failure on a create.
		return fmt.Errorf("%w: %s %s: server error %d", ErrAmbiguous, method, path, res.StatusCode)
	case res.StatusCode < 200 || res.StatusCode >= 300:
		var e hetznerError
		_ = json.Unmarshal(data, &e)
		if e.Error.Code == "" {
			e.Error.Code = "no detail"
		}
		return fmt.Errorf("%w: hetzner %s %s: API error %d (%s)", ErrDNS, method, path, res.StatusCode, e.Error.Code)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%w: %s %s: decode response: %v", ErrAmbiguous, method, path, err)
		}
	}
	return nil
}

var _ DNSProvider = (*HetznerClient)(nil)
