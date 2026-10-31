package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/ozal-project/ozal/internal/config"
)

// Permissions are the only ones the App asks for: list the chosen
// repositories, and open one pull request that adds a workflow. Webhooks and
// events stay off; Ozal polls nothing and receives nothing from GitHub.
var Permissions = map[string]string{"metadata": "read", "contents": "write", "workflows": "write", "pull_requests": "write"}

var (
	accountName  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	appSlug      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	manifestCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// ManifestRequest is what the console sends GitHub to create a private App.
// Origin is where the operator's browser reaches this console: GitHub sends
// the browser back there, never to Ozal's public listener.
type ManifestRequest struct {
	Name         string
	Origin       string
	Organization string
	State        string
}

// Manifest returns the URL to POST to and the manifest form value. It is pure:
// no request reaches GitHub until the operator's browser submits the form.
func Manifest(req ManifestRequest) (string, string, error) {
	origin, err := url.Parse(req.Origin)
	if err != nil || origin.Scheme != "https" && origin.Scheme != "http" || origin.Host == "" || origin.User != nil ||
		origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.Opaque != "" {
		return "", "", errors.New("origin must be the console's scheme and host, such as https://console.example.com")
	}
	if req.Organization != "" && !accountName.MatchString(req.Organization) {
		return "", "", errors.New("organization must be a GitHub account name")
	}
	if req.State == "" || req.Name == "" || len(req.Name) > 34 {
		return "", "", errors.New("invalid manifest request")
	}
	back := origin.Scheme + "://" + origin.Host + "/applications"
	manifest, err := json.Marshal(map[string]any{
		"name":                req.Name,
		"url":                 "https://ozal.dev",
		"hook_attributes":     map[string]any{"url": "https://ozal.dev/", "active": false},
		"redirect_url":        back,
		"setup_url":           back,
		"setup_on_update":     true,
		"public":              false,
		"default_permissions": Permissions,
		"default_events":      []string{},
	})
	if err != nil {
		return "", "", err
	}
	target := "https://github.com/settings/apps/new"
	if req.Organization != "" {
		target = "https://github.com/organizations/" + req.Organization + "/settings/apps/new"
	}
	return target + "?state=" + url.QueryEscape(req.State), string(manifest), nil
}

// CreatedApp is what GitHub returns once, when it creates an App from a
// manifest. Only the key is kept; the OAuth client secret and webhook secret
// in the same response are dropped here and never leave this package.
type CreatedApp struct {
	ID    int64
	Slug  string
	Owner string
	Key   []byte
}

// ConvertManifest exchanges the one-use code from GitHub's redirect for the
// new App's ID and private key. The code itself is the credential: GitHub
// accepts it once, within an hour.
func (c *Client) ConvertManifest(ctx context.Context, code string) (CreatedApp, error) {
	if !manifestCode.MatchString(code) {
		return CreatedApp{}, ErrUnavailable
	}
	var created struct {
		ID    int64  `json:"id"`
		Slug  string `json:"slug"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		PEM string `json:"pem"`
	}
	if err := c.call(ctx, http.MethodPost, "/app-manifests/"+code+"/conversions", "", nil, &created); err != nil {
		return CreatedApp{}, ErrUnavailable
	}
	if created.ID < 1 || !appSlug.MatchString(created.Slug) || !accountName.MatchString(created.Owner.Login) {
		return CreatedApp{}, ErrUnavailable
	}
	key := []byte(strings.TrimSpace(created.PEM) + "\n")
	if _, err := parseKey(key); err != nil {
		return CreatedApp{}, ErrUnavailable
	}
	return CreatedApp{ID: created.ID, Slug: created.Slug, Owner: created.Owner.Login, Key: key}, nil
}

// AppStatus describes the configured App: its public name, where to install
// it, and its installation if there is exactly one. A private App can only be
// installed on the account that owns it, so a second one is refused.
type AppStatus struct {
	Slug         string
	Owner        string
	Installation int64
}

func (s AppStatus) InstallURL() string {
	return "https://github.com/apps/" + s.Slug + "/installations/new"
}

func (c *Client) AppStatus(ctx context.Context, cfg config.GitHubApp) (AppStatus, error) {
	token, err := c.appJWT(cfg)
	if err != nil {
		return AppStatus{}, ErrUnavailable
	}
	var app struct {
		ID    int64  `json:"id"`
		Slug  string `json:"slug"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := c.call(ctx, http.MethodGet, "/app", token, nil, &app); err != nil || app.ID != cfg.AppID || !appSlug.MatchString(app.Slug) {
		return AppStatus{}, ErrUnavailable
	}
	var installations []struct {
		ID int64 `json:"id"`
	}
	if err := c.call(ctx, http.MethodGet, "/app/installations?per_page=2", token, nil, &installations); err != nil || len(installations) > 1 {
		return AppStatus{}, ErrUnavailable
	}
	status := AppStatus{Slug: app.Slug, Owner: app.Owner.Login}
	if len(installations) == 1 {
		if installations[0].ID < 1 {
			return AppStatus{}, ErrUnavailable
		}
		status.Installation = installations[0].ID
	}
	return status, nil
}
