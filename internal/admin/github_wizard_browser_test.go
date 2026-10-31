package admin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/githubapp"
	"net/http/httptest"
)

// wizardManager applies the wizard's three writes (route, project trust,
// GitHub installation) and moves its digest on each, like the route manager.
type wizardManager struct {
	*fakeManager
	revision int
}

func (m *wizardManager) ConfigDigest() string { return fmt.Sprintf("revision-%d", m.revision) }
func (m *wizardManager) PutApplication(route config.Route, expected string) error {
	if expected != m.ConfigDigest() {
		return errConfigChangedForTest
	}
	m.cfg.Routes = append(slices.DeleteFunc(m.cfg.Routes, func(r config.Route) bool { return r.Host == route.Host }), route)
	m.revision++
	return nil
}
func (m *wizardManager) PutProject(project config.Project, expected string, _ bool) error {
	if expected != m.ConfigDigest() {
		return errConfigChangedForTest
	}
	m.cfg.Projects = append(m.cfg.Projects, project)
	m.revision++
	return nil
}
func (m *wizardManager) ConfigureGitHub(app *config.GitHubApp, expected string) error {
	if expected != m.ConfigDigest() {
		return errConfigChangedForTest
	}
	m.cfg.GitHub = app
	m.revision++
	return nil
}

type wizardGitHub struct{ *workflowRepositories }

func (wizardGitHub) ConvertManifest(context.Context, string) (githubapp.CreatedApp, error) {
	return githubapp.CreatedApp{}, githubapp.ErrUnavailable
}
func (wizardGitHub) AppStatus(context.Context, config.GitHubApp) (githubapp.AppStatus, error) {
	return githubapp.AppStatus{Slug: "ozal-abc123", Owner: "octavia", Installation: 42}, nil
}

// The operator comes back from installing the App on GitHub, picks the
// repository and saves once: route, publisher trust and pull request. Only
// GitHub I/O is simulated; no request leaves loopback.
func TestGitHubWizardBrowserAcceptance(t *testing.T) {
	if os.Getenv("OZAL_WORKFLOW_BROWSER") != "1" {
		t.Skip("set OZAL_WORKFLOW_BROWSER=1 with agent-browser and Chrome installed")
	}
	_, fixture, repositories, _ := workflowFixture(t)
	cfg := fixture.cfg
	cfg.Routes, cfg.Projects = nil, nil
	cfg.GitHub = &config.GitHubApp{AppID: 7, PrivateKey: "file:/private/reference"}
	manager := &wizardManager{fakeManager: &fakeManager{cfg: cfg}}
	broker := events.NewBroker()
	h := New(manager, broker, WithGitHubRepositories(wizardGitHub{repositories}), WithGitHubKeyDir(t.TempDir()), WithPages(&workflowBrowserSites{}), WithEvents(broker))
	server := httptest.NewServer(h)
	defer server.Close()
	oneUse, _, err := h.operators.issue("Wizard acceptance", true)
	if err != nil {
		t.Fatal(err)
	}
	// A second edge with GitHub not connected shows how to connect it. The
	// browser never submits that form: it would leave loopback for GitHub.
	unconnected := cfg
	unconnected.GitHub = nil
	bareEvents := events.NewBroker()
	bare := New(&wizardManager{fakeManager: &fakeManager{cfg: unconnected}}, bareEvents, WithGitHubRepositories(wizardGitHub{repositories}), WithGitHubKeyDir(t.TempDir()), WithPages(&workflowBrowserSites{}), WithEvents(bareEvents))
	connectServer := httptest.NewServer(bare)
	defer connectServer.Close()
	connectLogin, _, err := bare.operators.issue("Wizard acceptance", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/demo/github-wizard-acceptance.mjs")
	cmd.Env = append(os.Environ(), "OZAL_WORKFLOW_URL="+server.URL+"/applications?setup_action=install&installation_id=999#operator="+oneUse, "OZAL_CONNECT_URL="+connectServer.URL+"/applications#operator="+connectLogin)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("GitHub wizard browser acceptance: %v\n%s", err, output)
	}
	t.Log(string(output))
	if manager.cfg.GitHub.InstallationID != 42 {
		t.Fatalf("installation %d; want the one GitHub reported, not the URL's", manager.cfg.GitHub.InstallationID)
	}
	if len(manager.cfg.Routes) != 1 || manager.cfg.Routes[0].Site != "www-example-test" || manager.cfg.Routes[0].Access != nil {
		t.Fatalf("routes %+v", manager.cfg.Routes)
	}
	if len(manager.cfg.Projects) != 1 || manager.cfg.Projects[0].Deployers[0].GitHub.WorkflowRef != "octavia/website/.github/workflows/ozal-pages.yml@refs/heads/main" {
		t.Fatalf("projects %+v", manager.cfg.Projects)
	}
	if repositories.writes != 1 {
		t.Fatalf("browser opened %d pull requests; want one", repositories.writes)
	}
}
