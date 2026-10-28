// Package githubapp is the optional GitHub metadata adapter. It does not
// build, clone, write workflows, authenticate visitors, or import teams.
package githubapp

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/switchboard-project/switchboard/internal/config"
)

var ErrUnavailable = errors.New("GitHub metadata unavailable; check the installation, key reference, rate limit and selected repositories")

type Repository struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Owner    struct {
		ID int64 `json:"id"`
	} `json:"owner"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}
type Listing struct {
	Repositories []Repository `json:"repositories"`
	Total        int          `json:"total_count"`
	Page         int          `json:"page"`
	More         bool         `json:"more"`
}
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Now     func() time.Time
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, BaseURL: "https://api.github.com", Now: time.Now}
}

func (c *Client) Repositories(ctx context.Context, cfg config.GitHubApp, page int) (Listing, error) {
	if page < 1 || page > 1000 {
		return Listing{}, ErrUnavailable
	}
	secret, err := config.ResolveSecret(cfg.PrivateKey)
	if err != nil {
		return Listing{}, ErrUnavailable
	}
	key, err := parseKey([]byte(secret))
	if err != nil {
		return Listing{}, ErrUnavailable
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return Listing{}, ErrUnavailable
	}
	now := c.Now()
	token, err := jwt.Signed(signer).Claims(jwt.Claims{Issuer: strconv.FormatInt(cfg.AppID, 10), IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), Expiry: jwt.NewNumericDate(now.Add(5 * time.Minute))}).Serialize()
	if err != nil {
		return Listing{}, ErrUnavailable
	}
	// Even if the installed App has broader permissions, this token asks only
	// for metadata. It stays in memory and is never sent to the browser.
	var installation struct {
		Token string `json:"token"`
	}
	if err := c.call(ctx, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", cfg.InstallationID), token, []byte(`{"permissions":{"metadata":"read"}}`), &installation); err != nil || installation.Token == "" {
		return Listing{}, ErrUnavailable
	}
	var listing Listing
	if err := c.call(ctx, http.MethodGet, fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page), installation.Token, nil, &listing); err != nil {
		return Listing{}, ErrUnavailable
	}
	for _, repo := range listing.Repositories {
		if repo.ID < 1 || repo.Owner.ID < 1 || !validFullName(repo.FullName) {
			return Listing{}, ErrUnavailable
		}
	}
	if listing.Repositories == nil {
		listing.Repositories = []Repository{}
	}
	listing.Page = page
	listing.More = page*100 < listing.Total
	return listing, nil
}
func validFullName(value string) bool {
	if strings.Count(value, "/") != 1 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-", r)) {
			return false
		}
	}
	return true
}
func parseKey(raw []byte) (*rsa.PrivateKey, error) {
	block, rest := pem.Decode(raw)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrUnavailable
	}
	var key *rsa.PrivateKey
	if block.Type == "RSA PRIVATE KEY" {
		parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		key = parsed
	} else if block.Type == "PRIVATE KEY" {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		var ok bool
		key, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, ErrUnavailable
		}
	} else {
		return nil, ErrUnavailable
	}
	if key.N.BitLen() < 2048 {
		return nil, ErrUnavailable
	}
	return key, nil
}
func (c *Client) call(ctx context.Context, method, path, token string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "switchboard")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return ErrUnavailable
	}
	limited := io.LimitReader(res.Body, 2<<20)
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return ErrUnavailable
	}
	return nil
}
