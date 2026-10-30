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
	return socketCommand(e, "ozal project trust", 3, args)
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
	code, err := runOnSocket(ctx, remote, e, ProjectTrustCommand(e, r))
	if err != nil {
		return err
	}
	if code == 3 {
		return errors.New("the edge has no `project trust` yet: run `ozalctl edge upgrade -apply` first; nothing changed")
	}
	if code != 0 {
		return fmt.Errorf("project trust ended with status %d; inspect the plan before retrying", code)
	}
	return nil
}

// ProjectRenameRequest is one run of `ozalctl project rename`.
type ProjectRenameRequest struct {
	Name, Project, To string
	Target            Target
	Apply             bool
}

// ProjectRenameCommand runs `ozal project rename` on the edge against its
// admin socket. The guard refuses binaries that predate the subcommand.
func ProjectRenameCommand(e Edge, r ProjectRenameRequest) string {
	args := []string{"project", "rename", "-admin", "unix:" + e.Socket, "-project", r.Project, "-to", r.To}
	if r.Apply {
		args = append(args, "-apply")
	}
	return socketCommand(e, "ozal project rename", 3, args)
}

// ProjectRename prints the edge's rename plan and, with Apply, renames.
func ProjectRename(ctx context.Context, out io.Writer, remote Remote, r ProjectRenameRequest) error {
	e := r.Target.Edge
	if !pages.ValidSiteName(r.Project) || !pages.ValidSiteName(r.To) || e.Socket == "" || e.Binary == "" || e.ServiceUser == "" {
		return errors.New("valid -project and -to names and edge.socket, binary and service_user are required")
	}
	fmt.Fprintf(out, "==> %s: rename project %s to %s (apply=%t)\n", r.Name, r.Project, r.To, r.Apply)
	code, err := runOnSocket(ctx, remote, e, ProjectRenameCommand(e, r))
	if err != nil {
		return err
	}
	if code == 3 {
		return errors.New("the edge has no `project rename` yet: run `ozalctl edge upgrade -apply` first; nothing changed")
	}
	if code != 0 {
		return fmt.Errorf("project rename ended with status %d; read its output above before retrying", code)
	}
	return nil
}
