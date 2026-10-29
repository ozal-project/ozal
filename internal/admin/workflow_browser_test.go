package admin

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/pages"
)

type workflowBrowserSites struct{ fakeDeployments }

func (workflowBrowserSites) Sites(context.Context) ([]pages.Site, error) {
	return []pages.Site{{Name: "blog", Live: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ChangedAt: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), Deployments: []pages.Deployment{{Site: "blog", ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Live: true, Files: 2, Bytes: 512, CreatedAt: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}}}}, nil
}

// This optional acceptance runs the production embedded SPA against the real
// admin handlers and pure generator. Only GitHub I/O and deployment data are
// fixtures. No GitHub credential, repository or public listener is used.
func TestWorkflowBrowserAcceptance(t *testing.T) {
	if os.Getenv("OZAL_WORKFLOW_BROWSER") != "1" {
		t.Skip("set OZAL_WORKFLOW_BROWSER=1 with agent-browser and Chrome installed")
	}
	_, manager, repositories, _ := workflowFixture(t)
	broker := events.NewBroker()
	h := New(manager, broker, WithGitHubRepositories(repositories), WithPages(&workflowBrowserSites{}), WithEvents(broker))
	server := httptest.NewServer(h)
	defer server.Close()
	oneUse, _, err := h.operators.issue("Workflow acceptance", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/demo/workflow-acceptance.mjs")
	cmd.Env = append(os.Environ(), "OZAL_WORKFLOW_URL="+server.URL+"/sites#operator="+oneUse)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workflow browser acceptance: %v\n%s", err, output)
	}
	t.Log(string(output))
	if repositories.writes != 1 {
		t.Fatalf("browser wrote %d proposals; want exactly one after explicit confirmation", repositories.writes)
	}
}
