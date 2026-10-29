package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/pages"
)

type deployConfigManager struct{ cfg config.Config }

func (m *deployConfigManager) Config() config.Config { return config.Clone(m.cfg) }
func (*deployConfigManager) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

type rootDeployStore struct{}

func (rootDeployStore) DeployProject(_ context.Context, site string, source pages.SourceMetadata, body io.Reader) (pages.Deployment, error) {
	_, err := io.ReadAll(body)
	return pages.Deployment{Site: site, Project: source.Project, ID: strings.Repeat("a", 64), Live: true}, err
}
func (rootDeployStore) Deployment(context.Context, string, string) (pages.Deployment, error) {
	return pages.Deployment{}, pages.ErrNotFound
}

type rootReplayStore struct{}

func (rootReplayStore) Consume(context.Context, string, time.Time) (bool, error) { return true, nil }

func TestPublicDeployHostIsIsolatedAndProjectTrustFollowsReload(t *testing.T) {
	t.Setenv("ROOT_DEPLOY_SECRET", "initial-secret")
	t.Setenv("ROOT_NEW_DEPLOY_SECRET", "rotated-secret")
	m := &deployConfigManager{cfg: config.Config{Pages: &config.Pages{Deploy: &config.DeployEndpoint{Host: "deploy.localhost", Audience: "local-test"}}, Projects: []config.Project{{Name: "portfolio", Site: "portfolio", Deployers: []config.ProjectDeployer{{Token: "env:ROOT_DEPLOY_SECRET"}}}}}}
	h, err := publicPagesHandler(context.Background(), m.cfg, m, rootDeployStore{}, rootReplayStore{}, events.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(host, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://"+host+path, strings.NewReader("archive"))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, path := range []string{"/_/reload", "/_/api/projects", "/routes"} {
		if rec := request("deploy.localhost", path, "initial-secret"); rec.Code != 404 {
			t.Fatalf("dedicated host exposed %s: %d", path, rec.Code)
		}
	}
	path := "/v1/projects/portfolio/deployments"
	if rec := request("app.localhost", path, ""); rec.Code != 204 {
		t.Fatalf("application handler=%d", rec.Code)
	}
	if rec := request("deploy.localhost:443", path, "initial-secret"); rec.Code != 201 {
		t.Fatalf("deploy host with explicit port=%d %s", rec.Code, rec.Body)
	}
	m.cfg.Projects[0].Deployers[0].Token = "env:ROOT_NEW_DEPLOY_SECRET"
	if rec := request("deploy.localhost", path, "initial-secret"); rec.Code != 401 {
		t.Fatalf("old trust survived reload=%d", rec.Code)
	}
	if rec := request("deploy.localhost", path, "rotated-secret"); rec.Code != 201 {
		t.Fatalf("new trust not applied=%d %s", rec.Code, rec.Body)
	}
}
