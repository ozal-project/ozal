package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/pages"
)

// renameManager applies a rename to its in-memory config with the digest
// rule of the route manager, recording the order of calls.
type renameManager struct {
	*fakeManager
	digest string
	calls  *[]string
}

func (m *renameManager) ConfigDigest() string { return m.digest }
func (m *renameManager) RenameProject(from, to, expected string) error {
	*m.calls = append(*m.calls, "rename "+from+"->"+to)
	if expected != m.digest {
		return errors.New("stale")
	}
	next, _, _, err := config.PlanProjectRename(m.cfg, from, to)
	if err != nil {
		return err
	}
	m.cfg, m.digest = next, m.digest+"+"
	return nil
}

type copyingPages struct {
	fakeDeployments
	calls *[]string
	err   error
}

func (p *copyingPages) CopyLive(_ context.Context, from, to string) (pages.Deployment, error) {
	*p.calls = append(*p.calls, "copy "+from+"->"+to)
	return pages.Deployment{Site: to, ID: strings.Repeat("a", 64)}, p.err
}

func renameFixture(t *testing.T, copyErr error) (*Handler, *renameManager, *[]string) {
	t.Helper()
	calls := &[]string{}
	cfg := config.Config{Listen: "127.0.0.1:8888", Admin: config.Admin{Token: readToken}, Pages: &config.Pages{Dir: t.TempDir()},
		Routes:   []config.Route{{Host: "switchboard.example.com", Site: "switchboard-site"}, {Host: "ozal.example.com", Site: "switchboard-site"}},
		Projects: []config.Project{{Name: "switchboard-site", Site: "switchboard-site", Hostnames: []string{"switchboard.example.com", "ozal.example.com"}, Deployers: []config.ProjectDeployer{{Token: "env:T"}}}}}
	m := &renameManager{fakeManager: &fakeManager{cfg: cfg}, digest: "d1", calls: calls}
	return New(m, nil, WithPages(&copyingPages{calls: calls, err: copyErr})), m, calls
}

func renameRequest(h http.Handler, local bool, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if local {
		req = localRequest(http.MethodPost, "/_/api/projects/switchboard-site/rename")
		req.Body = io.NopCloser(strings.NewReader(body))
	} else {
		req = httptest.NewRequest(http.MethodPost, "/_/api/projects/switchboard-site/rename", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+readToken)
		req.Host = "127.0.0.1:9090"
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Renaming changes what CI publishes under: no bearer token may do it.
func TestProjectRenameIsHostOnly(t *testing.T) {
	h, m, calls := renameFixture(t, nil)
	if rec := renameRequest(h, false, `{"to":"ozal-site","expected":"d1","apply":true}`); rec.Code != http.StatusForbidden {
		t.Fatalf("token rename = %d", rec.Code)
	}
	if len(*calls) != 0 || m.cfg.Projects[0].Name != "switchboard-site" {
		t.Fatalf("a refused request changed something: %v", *calls)
	}
}

func TestProjectRenamePlansThenCopiesBeforeRenaming(t *testing.T) {
	h, m, calls := renameFixture(t, nil)
	rec := renameRequest(h, true, `{"to":"ozal-site","expected":"d1"}`)
	var plan struct {
		To    string   `json:"to"`
		Hosts []string `json:"hosts"`
		Apply bool     `json:"applied"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil || plan.To != "ozal-site" || len(plan.Hosts) != 2 || plan.Apply {
		t.Fatalf("plan = %d %s", rec.Code, rec.Body)
	}
	if len(*calls) != 0 {
		t.Fatalf("a plan changed something: %v", *calls)
	}
	if rec := renameRequest(h, true, `{"to":"ozal-site","expected":"stale","apply":true}`); rec.Code != http.StatusConflict || len(*calls) != 0 {
		t.Fatalf("stale apply = %d, calls %v", rec.Code, *calls)
	}
	rec = renameRequest(h, true, `{"to":"ozal-site","expected":"d1","apply":true}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"applied":true`) {
		t.Fatalf("apply = %d %s", rec.Code, rec.Body)
	}
	if strings.Join(*calls, "; ") != "copy switchboard-site->ozal-site; rename switchboard-site->ozal-site" {
		t.Fatalf("order = %v: the build must be copied before routes move", *calls)
	}
	if m.cfg.Routes[0].Site != "ozal-site" || m.cfg.Projects[0].Name != "ozal-site" {
		t.Fatalf("config = %+v", m.cfg)
	}
}

// Without a copied build the routes would serve nothing: no rename then.
func TestProjectRenameStopsWhenTheCopyFails(t *testing.T) {
	h, m, calls := renameFixture(t, pages.ErrNothingLive)
	rec := renameRequest(h, true, `{"to":"ozal-site","expected":"d1","apply":true}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "nothing live") {
		t.Fatalf("apply = %d %s", rec.Code, rec.Body)
	}
	if len(*calls) != 1 || m.cfg.Projects[0].Name != "switchboard-site" {
		t.Fatalf("renamed after a failed copy: %v", *calls)
	}
}
