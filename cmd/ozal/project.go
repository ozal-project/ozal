package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/ozal-project/ozal/internal/config"
)

type projectTrustResult struct {
	Name    string                    `json:"name"`
	Before  config.ProjectGitHubTrust `json:"before"`
	After   config.ProjectGitHubTrust `json:"after"`
	Changed bool                      `json:"changed"`
	Applied bool                      `json:"applied"`
}

// runProject migrates an explicit host-operator trust binding. Repository
// discovery remains App-verified; this separate command requires the socket.
func runProject(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "trust" {
		return errors.New("usage: ozal project trust -project NAME -repository-id ID -owner-id ID -ref REF -workflow-ref WORKFLOW [-apply -backup FILE]")
	}
	fs := flag.NewFlagSet("project trust", flag.ContinueOnError)
	fs.SetOutput(out)
	af := addAdminFlags(fs)
	name := fs.String("project", "", "existing project whose one GitHub publisher is being migrated")
	trust := config.ProjectGitHubTrust{}
	fs.Int64Var(&trust.RepositoryID, "repository-id", 0, "verified numeric GitHub repository ID")
	fs.Int64Var(&trust.RepositoryOwnerID, "owner-id", 0, "verified numeric GitHub owner ID")
	fs.StringVar(&trust.Ref, "ref", "refs/heads/main", "only ref allowed to deploy")
	fs.StringVar(&trust.WorkflowRef, "workflow-ref", "", "exact owner/repo/.github/workflows/file.yml@refs/heads/branch claim")
	apply := fs.Bool("apply", false, "save after reviewing the plan (default: no mutation)")
	backup := fs.String("backup", "", "new private file for previous trust metadata (required with -apply)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || *name == "" || trust.RepositoryID < 1 || trust.RepositoryOwnerID < 1 || trust.WorkflowRef == "" {
		return errors.New("project, positive repository/owner IDs and workflow-ref are required")
	}
	if *af.target != "" && !strings.HasPrefix(*af.target, "unix:") {
		return errors.New("publisher trust migration requires the host's Unix socket")
	}
	if *apply && *backup == "" {
		return errors.New("-apply requires -backup with a new private file path")
	}
	client, err := af.client()
	if err != nil {
		return err
	}
	return migrateProjectTrust(ctx, out, client, *name, trust, *apply, *backup)
}

func migrateProjectTrust(ctx context.Context, out io.Writer, client *adminClient, name string, trust config.ProjectGitHubTrust, apply bool, backup string) error {
	var setup struct {
		Expected string `json:"config_sha256"`
	}
	if err := client.do(ctx, http.MethodGet, "/_/api/setup", nil, &setup); err != nil {
		return err
	}
	endpoint := "/_/api/projects/" + url.PathEscape(name) + "/github-trust"
	payload := map[string]any{"trust": trust, "expected": setup.Expected, "apply": false}
	var plan projectTrustResult
	if err := client.do(ctx, http.MethodPut, endpoint, payload, &plan); err != nil {
		return err
	}
	if plan.Name != name || plan.After != trust || plan.Applied {
		return errors.New("edge returned an unexpected publisher trust plan")
	}
	fmt.Fprintf(out, "Project %s: repository %d / owner %d -> repository %d / owner %d\nWorkflow: %s\n",
		name, plan.Before.RepositoryID, plan.Before.RepositoryOwnerID, trust.RepositoryID, trust.RepositoryOwnerID, trust.WorkflowRef)
	if !plan.Changed {
		fmt.Fprintln(out, "Publisher trust already matches; nothing changed.")
		return nil
	}
	if !apply {
		fmt.Fprintln(out, "Plan only. Re-run with -apply and -backup to save.")
		return nil
	}
	// Only non-secret publisher metadata is kept. O_EXCL refuses both an
	// existing file and a symlink, so a prior recovery record is never replaced.
	data, err := json.MarshalIndent(struct {
		Project string                    `json:"project"`
		Before  config.ProjectGitHubTrust `json:"before"`
		After   config.ProjectGitHubTrust `json:"after"`
	}{name, plan.Before, trust}, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("cannot create a new private trust backup; nothing changed")
	}
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("trust backup failed; nothing changed")
	}
	payload["apply"] = true
	var saved projectTrustResult
	if err := client.do(ctx, http.MethodPut, endpoint, payload, &saved); err != nil {
		return fmt.Errorf("publisher trust was not confirmed; backup retained, inspect the plan before retrying: %w", err)
	}
	if saved.Name != name || !saved.Applied || saved.After != trust {
		return errors.New("publisher trust response did not confirm the reviewed migration; reconcile before retrying")
	}
	var live struct {
		Projects []struct {
			Name      string `json:"name"`
			Deployers []struct {
				GitHub *config.ProjectGitHubTrust `json:"github"`
			} `json:"deployers"`
		} `json:"projects"`
	}
	if err := client.do(ctx, http.MethodGet, "/_/api/projects", nil, &live); err != nil {
		return errors.New("publisher trust saved but live confirmation failed; inspect the plan before retrying")
	}
	for _, project := range live.Projects {
		if project.Name == name {
			for _, deployer := range project.Deployers {
				if deployer.GitHub != nil && *deployer.GitHub == trust {
					fmt.Fprintf(out, "Publisher trust confirmed live. Previous trust metadata: %s\n", backup)
					return nil
				}
			}
		}
	}
	return errors.New("live publisher trust differs from the reviewed migration; backup retained")
}
