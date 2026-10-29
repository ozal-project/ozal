package ctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/pages"
)

type ProjectTrustRequest struct {
	Name, Project, Stamp string
	Target               Target
	Trust                config.ProjectGitHubTrust
	Apply                bool
}

// ProjectTrustCommand delegates to the edge's API-backed host command, rather
// than editing its runtime externally. The binary guard refuses older edges.
func ProjectTrustCommand(e Edge, r ProjectTrustRequest) string {
	args := []string{"project", "trust", "-admin", "unix:" + e.Socket, "-project", r.Project,
		"-repository-id", strconv.FormatInt(r.Trust.RepositoryID, 10), "-owner-id", strconv.FormatInt(r.Trust.RepositoryOwnerID, 10),
		"-ref", r.Trust.Ref, "-workflow-ref", r.Trust.WorkflowRef}
	if r.Apply {
		backup := path.Join(path.Dir(e.StateDB), "project-trust-"+r.Project+"-"+r.Stamp+".json")
		args = append(args, "-apply", "-backup", backup)
	}
	for i := range args {
		args[i] = Quote(args[i])
	}
	return fmt.Sprintf("grep -qaF 'ozal project trust' %s || exit 3; sudo -u %s %s %s",
		Quote(e.Binary), Quote(e.ServiceUser), Quote(e.Binary), strings.Join(args, " "))
}

func ProjectTrust(ctx context.Context, out io.Writer, remote Remote, r ProjectTrustRequest) error {
	e := r.Target.Edge
	if !pages.ValidSiteName(r.Project) || e.Socket == "" || e.Binary == "" || e.ServiceUser == "" || !path.IsAbs(e.StateDB) {
		return errors.New("a valid project and edge.socket, binary, service_user and absolute state_db are required")
	}
	if err := config.ValidateProjectGitHubTrust(r.Trust); err != nil {
		return errors.New("invalid repository IDs, ref or workflow-ref")
	}
	if r.Apply && (r.Stamp == "" || strings.ContainsAny(r.Stamp, "/\\\r\n")) {
		return errors.New("a safe backup timestamp is required")
	}
	fmt.Fprintf(out, "==> %s: migrate GitHub publisher of %s (apply=%t)\n", r.Name, r.Project, r.Apply)
	code, err := remote.Terminal(ctx, ProjectTrustCommand(e, r))
	if err != nil {
		return fmt.Errorf("%w: run from your terminal so sudo can ask for the password", err)
	}
	if code == 3 {
		return errors.New("the edge has no `project trust` yet: run `ozalctl edge upgrade -apply` first; nothing changed")
	}
	if code != 0 {
		return fmt.Errorf("project trust ended with status %d; inspect the plan before retrying", code)
	}
	return nil
}
