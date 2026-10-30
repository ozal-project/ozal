package ctl

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

type trustRemote struct {
	command string
	code    int
	err     error
}

func (r *trustRemote) Run(context.Context, string, io.Reader) (string, error) {
	return "", errors.New("unexpected non-terminal operation")
}
func (r *trustRemote) Stream(_ context.Context, command string) (int, error) {
	r.command = "STREAM " + command
	return r.code, r.err
}
func (r *trustRemote) Terminal(_ context.Context, command string) (int, error) {
	r.command = command
	return r.code, r.err
}

func TestProjectTrustOperatorGuardAndRecoveryPath(t *testing.T) {
	target := goodTarget()
	target.Edge.Socket = "/run/ozal/admin.sock"
	r := ProjectTrustRequest{Name: "edge", Target: target, Project: "site", Stamp: "20261029T120000Z", Trust: config.ProjectGitHubTrust{RepositoryID: 3, RepositoryOwnerID: 4, Ref: "refs/heads/main", WorkflowRef: "new/repo/.github/workflows/site.yml@refs/heads/main"}}
	remote := &trustRemote{}
	if err := ProjectTrust(context.Background(), io.Discard, remote, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.command, "grep -qaF 'ozal project trust'") || strings.Contains(remote.command, "'-apply'") {
		t.Fatal("plan lacks guard or mutates")
	}
	r.Apply = true
	if err := ProjectTrust(context.Background(), io.Discard, remote, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.command, "'-backup' '/var/lib/ozal/project-trust-site-20261029T120000Z.json'") {
		t.Fatal("backup is not under service-owned state")
	}
	remote.code = 3
	if err := ProjectTrust(context.Background(), io.Discard, remote, r); err == nil || !strings.Contains(err.Error(), "edge upgrade") {
		t.Fatal("old edge did not require upgrade")
	}
	remote.command = ""
	r.Trust.RepositoryID = 0
	if err := ProjectTrust(context.Background(), io.Discard, remote, r); err == nil || remote.command != "" {
		t.Fatal("invalid IDs reached sudo")
	}
}

func TestProjectRenameRunsOnTheSocketWithGuard(t *testing.T) {
	target := goodTarget()
	target.Edge.Socket = "/run/ozal/admin.sock"
	remote := &trustRemote{}
	r := ProjectRenameRequest{Name: "edge", Target: target, Project: "switchboard-site", To: "ozal-site", Apply: true}
	if err := ProjectRename(context.Background(), io.Discard, remote, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.command, "grep -qaF 'ozal project rename'") || !strings.Contains(remote.command, "'project' 'rename' '-admin' 'unix:/run/ozal/admin.sock' '-project' 'switchboard-site' '-to' 'ozal-site' '-apply'") {
		t.Fatalf("command = %s", remote.command)
	}
	remote = &trustRemote{code: 3}
	if err := ProjectRename(context.Background(), io.Discard, remote, r); err == nil || !strings.Contains(err.Error(), "edge upgrade") {
		t.Fatalf("old binary: %v", err)
	}
	if err := ProjectRename(context.Background(), io.Discard, &trustRemote{}, ProjectRenameRequest{Target: target, Project: "a", To: "Bad Name"}); err == nil {
		t.Fatal("invalid name accepted")
	}
}
