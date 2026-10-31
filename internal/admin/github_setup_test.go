package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/githubapp"
	"github.com/ozal-project/ozal/internal/pages/workflow"
)

const setupKey = "-----BEGIN RSA PRIVATE KEY-----\nkey material\n-----END RSA PRIVATE KEY-----\n"

type setupGitHub struct {
	testRepositories
	conversions  int
	installation int64
	everyRepo    bool
	afterConvert func()
}

func (s *setupGitHub) ConvertManifest(_ context.Context, code string) (githubapp.CreatedApp, error) {
	s.conversions++
	if s.afterConvert != nil {
		s.afterConvert()
	}
	return githubapp.CreatedApp{ID: 77, Slug: "ozal-abc123", Owner: "octo", Key: []byte(setupKey)}, nil
}

func (s *setupGitHub) AppStatus(context.Context, config.GitHubApp) (githubapp.AppStatus, error) {
	return githubapp.AppStatus{Slug: "ozal-abc123", Owner: "octo", Installation: s.installation, AllRepositories: s.everyRepo, ManageURL: "https://github.com/settings/installations/42"}, nil
}

type githubManager struct {
	*fakeManager
	configured int
}

func (m *githubManager) ConfigureGitHub(app *config.GitHubApp, expected string) error {
	if expected != m.ConfigDigest() {
		return errConfigChangedForTest
	}
	m.configured++
	m.cfg.GitHub = app
	return nil
}

var errConfigChangedForTest = os.ErrInvalid

func setupFixture(t *testing.T) (*Handler, *githubManager, *setupGitHub, string) {
	t.Helper()
	dir := t.TempDir()
	manager := &githubManager{fakeManager: &fakeManager{cfg: config.Config{Listen: "127.0.0.1:8888", Admin: config.Admin{Token: "admin-test"}, Pages: &config.Pages{Dir: t.TempDir()}}}}
	setup := &setupGitHub{}
	return New(manager, nil, WithGitHubRepositories(setup), WithGitHubKeyDir(dir)), manager, setup, dir
}

func startManifest(t *testing.T, h *Handler, token string) string {
	t.Helper()
	rec := request(h, http.MethodPost, "/_/api/github/manifest", token, `{"origin":"https://console.example.com"}`)
	if rec.Code != 200 {
		t.Fatalf("manifest: %d %s", rec.Code, rec.Body)
	}
	var started struct{ URL, Manifest string }
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(started.URL)
	if err != nil || target.Host != "github.com" || !strings.Contains(started.Manifest, `"public":false`) {
		t.Fatalf("manifest target: %s", started.URL)
	}
	return target.Query().Get("state")
}

func convert(h *Handler, m *githubManager, token, state string) int {
	body, _ := json.Marshal(map[string]string{"code": "abc123", "state": state, "expected": m.ConfigDigest()})
	return request(h, http.MethodPost, "/_/api/github/manifest/conversion", token, string(body)).Code
}

func TestConsoleConnectsGitHubKeepingOnlyAKeyReference(t *testing.T) {
	h, m, setup, dir := setupFixture(t)
	token, _, err := h.operators.issue("Publisher", false, "publishing")
	if err != nil {
		t.Fatal(err)
	}
	state := startManifest(t, h, token)
	body, _ := json.Marshal(map[string]string{"code": "abc123", "state": state, "expected": m.ConfigDigest()})
	rec := request(h, http.MethodPost, "/_/api/github/manifest/conversion", token, string(body))
	if rec.Code != 200 {
		t.Fatalf("conversion: %d %s", rec.Code, rec.Body)
	}
	path := filepath.Join(dir, "github-app-77.pem")
	if got := m.cfg.GitHub; got == nil || got.AppID != 77 || got.InstallationID != 0 || got.PrivateKey != "file:"+path {
		t.Fatalf("configured %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", info, err)
	}
	if data, _ := os.ReadFile(path); string(data) != setupKey {
		t.Fatal("key file content")
	}
	if strings.Contains(rec.Body.String(), "PRIVATE KEY") || strings.Contains(rec.Body.String(), dir) {
		t.Fatal("response exposed the key or its location")
	}
	if !strings.Contains(rec.Body.String(), `"install_url":"https://github.com/apps/ozal-abc123/installations/new"`) {
		t.Fatalf("no installation link: %s", rec.Body)
	}
	// The same redirect replayed connects nothing.
	m.cfg.GitHub = nil
	if code := convert(h, m, token, state); code != 403 || setup.conversions != 1 {
		t.Fatalf("replayed state: %d conversions=%d", code, setup.conversions)
	}
}

func TestGitHubRedirectOnlyCompletesForTheSessionThatStartedIt(t *testing.T) {
	h, m, setup, _ := setupFixture(t)
	alice, _, _ := h.operators.issue("Alice", false, "publishing")
	mallory, _, _ := h.operators.issue("Mallory", false, "publishing")
	state := startManifest(t, h, alice)
	if code := convert(h, m, mallory, state); code != 403 {
		t.Fatalf("another session completed the connection: %d", code)
	}
	if code := convert(h, m, alice, state); code != 403 {
		t.Fatal("a state tried by another session stayed usable")
	}
	if code := convert(h, m, alice, "made-up"); code != 403 {
		t.Fatal("unknown state accepted")
	}
	expired := startManifest(t, h, alice)
	h.githubStates.mu.Lock()
	p := h.githubStates.pending[expired]
	p.expires = time.Now().Add(-time.Second)
	h.githubStates.pending[expired] = p
	h.githubStates.mu.Unlock()
	if code := convert(h, m, alice, expired); code != 403 {
		t.Fatal("expired state accepted")
	}
	if setup.conversions != 0 || m.cfg.GitHub != nil {
		t.Fatal("a refused redirect reached GitHub or the configuration")
	}
}

func TestConnectingGitHubNeedsThePublishingTool(t *testing.T) {
	h, m, setup, _ := setupFixture(t)
	people, _, _ := h.operators.issue("People", false, "people")
	for _, path := range []string{"/_/api/github/manifest", "/_/api/github/manifest/conversion", "/_/api/github/installation", "/_/api/github/workflow"} {
		if rec := request(h, http.MethodPost, path, people, `{}`); rec.Code != http.StatusForbidden {
			t.Fatalf("%s without publishing: %d", path, rec.Code)
		}
	}
	if setup.conversions != 0 || m.configured != 0 {
		t.Fatal("refused request changed state")
	}
}

func TestConnectionRevokedDuringExchangeWritesNothing(t *testing.T) {
	h, m, setup, dir := setupFixture(t)
	token, grant, _ := h.operators.issue("Publisher", false, "publishing")
	state := startManifest(t, h, token)
	setup.afterConvert = func() { h.operators.revoke(grant.ID) }
	if code := convert(h, m, token, state); code != http.StatusUnauthorized {
		t.Fatalf("revoked session: %d", code)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 || m.cfg.GitHub != nil {
		t.Fatal("revoked session stored a key or configuration")
	}
}

func TestConnectingNeverReplacesAKeyOrAConnectedApp(t *testing.T) {
	h, m, _, dir := setupFixture(t)
	token, _, _ := h.operators.issue("Publisher", false, "publishing")
	existing := filepath.Join(dir, "github-app-77.pem")
	if err := os.WriteFile(existing, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := convert(h, m, token, startManifest(t, h, token)); code != 500 {
		t.Fatalf("existing key file: %d", code)
	}
	if data, _ := os.ReadFile(existing); string(data) != "previous" || m.cfg.GitHub != nil {
		t.Fatal("replaced a key file")
	}
	m.cfg.GitHub = &config.GitHubApp{AppID: 1, InstallationID: 2, PrivateKey: "file:/elsewhere"}
	if rec := request(h, http.MethodPost, "/_/api/github/manifest", token, `{"origin":"https://console.example.com"}`); rec.Code != 409 {
		t.Fatalf("connected twice: %d", rec.Code)
	}
	bare := New(m, nil, WithGitHubRepositories(&setupGitHub{}))
	m.cfg.GitHub = nil
	if rec := request(bare, http.MethodPost, "/_/api/github/manifest", "admin-test", `{"origin":"https://console.example.com"}`); rec.Code != 422 {
		t.Fatalf("connected without a key directory: %d", rec.Code)
	}
}

func TestInstallationComesFromGitHubNotTheBrowser(t *testing.T) {
	h, m, setup, _ := setupFixture(t)
	token, _, _ := h.operators.issue("Publisher", false, "publishing")
	m.cfg.GitHub = &config.GitHubApp{AppID: 77, PrivateKey: "file:/private/reference"}
	rec := request(h, http.MethodGet, "/_/api/github/repositories", token, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"installed":false`) || !strings.Contains(rec.Body.String(), "/apps/ozal-abc123/installations/new") || strings.Contains(rec.Body.String(), "/private/reference") {
		t.Fatalf("uninstalled state: %d %s", rec.Code, rec.Body)
	}
	if rec := request(h, http.MethodPost, "/_/api/github/installation", token, `{"installation_id":999,"expected":"`+m.ConfigDigest()+`"}`); rec.Code != 400 || m.cfg.GitHub.InstallationID != 0 {
		t.Fatalf("accepted an installation named by the browser: %d", rec.Code)
	}
	body := `{"expected":"` + m.ConfigDigest() + `"}`
	if rec := request(h, http.MethodPost, "/_/api/github/installation", token, body); rec.Code != 409 || m.cfg.GitHub.InstallationID != 0 {
		t.Fatalf("bound an installation GitHub does not report: %d", rec.Code)
	}
	setup.installation = 42
	if rec := request(h, http.MethodPost, "/_/api/github/installation", token, body); rec.Code != 200 || m.cfg.GitHub.InstallationID != 42 {
		t.Fatalf("installation: %d %s", rec.Code, rec.Body)
	}
}

func TestRepositoryListingSaysHowWideTheInstallationIs(t *testing.T) {
	h, m, setup, _ := setupFixture(t)
	token, _, _ := h.operators.issue("Publisher", false, "publishing")
	m.cfg.GitHub = &config.GitHubApp{AppID: 77, InstallationID: 42, PrivateKey: "file:/private/reference"}
	setup.installation, setup.everyRepo = 42, true
	rec := request(h, http.MethodGet, "/_/api/github/repositories", token, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"all_repositories":true`) || !strings.Contains(rec.Body.String(), `"manage_url":"https://github.com/settings/installations/42"`) || !strings.Contains(rec.Body.String(), `"owner":"octo"`) {
		t.Fatalf("installed state: %d %s", rec.Code, rec.Body)
	}
	// GitHub now reports another installation than the configured one: the
	// listing stands, but nothing about the other one is described as ours.
	setup.installation = 43
	rec = request(h, http.MethodGet, "/_/api/github/repositories", token, "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "all_repositories") || strings.Contains(rec.Body.String(), "manage_url") {
		t.Fatalf("described another installation: %d %s", rec.Code, rec.Body)
	}
}

func TestWizardPreviewMatchesTheWorkflowLaterProposed(t *testing.T) {
	h, _, _, _ := workflowFixture(t)
	token, _, _ := h.operators.issue("Publisher", false, "publishing")
	rec := request(h, http.MethodPost, "/_/api/github/workflow", token, `{"project":"blog","repository_id":123,"ref":"refs/heads/main","workflow":"ozal.yml","hostname":"blog.example.test","directory":".","output":"dist"}`)
	if rec.Code != 200 {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	var preview struct{ Plan workflow.Plan }
	json.Unmarshal(rec.Body.Bytes(), &preview)
	rec = request(h, http.MethodPost, "/_/api/projects/blog/workflow", token, `{"directory":".","output":"dist"}`)
	var later struct{ Plan workflow.Plan }
	json.Unmarshal(rec.Body.Bytes(), &later)
	if preview.Plan.Digest == "" || preview.Plan.Digest != later.Plan.Digest {
		t.Fatal("the reviewed preview differs from the workflow the pull request would add")
	}
	if rec := request(h, http.MethodPost, "/_/api/github/workflow", token, `{"project":"blog","repository_id":999,"ref":"refs/heads/main","workflow":"ozal.yml","hostname":"blog.example.test","directory":".","output":"dist"}`); rec.Code != 403 {
		t.Fatalf("previewed a repository outside the installation: %d", rec.Code)
	}
}
