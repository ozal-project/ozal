package config

import (
	"reflect"
	"testing"
)

func TestProjectTrustPreservesOtherAuthorityAndDoesNotMutateInput(t *testing.T) {
	old := ProjectGitHubTrust{RepositoryID: 1, RepositoryOwnerID: 2, Ref: "refs/heads/main", WorkflowRef: "old/repo/.github/workflows/site.yml@refs/heads/main"}
	next := old
	next.RepositoryID, next.RepositoryOwnerID, next.WorkflowRef = 3, 4, "new/repo/.github/workflows/site.yml@refs/heads/main"
	cfg := Config{Listen: "127.0.0.1:8888", Pages: &Pages{Dir: "/pages"}, Routes: []Route{{Host: "site.localhost", Site: "site"}},
		Projects: []Project{{Name: "site", Site: "site", Hostnames: []string{"site.localhost"},
			Deployers: []ProjectDeployer{{GitHub: &old}, {Token: "file:/private/reference"}}}}}
	before := Clone(cfg)
	project, previous, err := PlanProjectTrust(cfg, "site", next)
	if err != nil {
		t.Fatal(err)
	}
	if previous != old || *project.Deployers[0].GitHub != next || project.Deployers[1].Token != "file:/private/reference" || !reflect.DeepEqual(cfg, before) {
		t.Fatal("migration changed or lost existing authority")
	}
	project.Hostnames[0] = "other.localhost"
	if !reflect.DeepEqual(cfg, before) {
		t.Fatal("candidate aliases the live configuration")
	}
	for _, name := range []string{"missing", ""} {
		if _, _, err := PlanProjectTrust(cfg, name, next); err == nil {
			t.Fatal("missing project accepted")
		}
	}
	for _, mutate := range []func(*ProjectGitHubTrust){
		func(x *ProjectGitHubTrust) { x.RepositoryID = 0 },
		func(x *ProjectGitHubTrust) { x.RepositoryOwnerID = 0 },
		func(x *ProjectGitHubTrust) { x.Ref = "refs/pull/1/merge" },
		func(x *ProjectGitHubTrust) { x.WorkflowRef = "new/repo/site.yml@refs/heads/main" },
	} {
		bad := next
		mutate(&bad)
		if _, _, err := PlanProjectTrust(cfg, "site", bad); err == nil {
			t.Fatal("invalid trust accepted")
		}
	}
	for _, deployers := range [][]ProjectDeployer{{{Token: "file:/private/reference"}}, {{GitHub: &old}, {GitHub: &next}}} {
		ambiguous := Clone(cfg)
		ambiguous.Projects[0].Deployers = deployers
		if _, _, err := PlanProjectTrust(ambiguous, "site", next); err == nil {
			t.Fatal("project without one unambiguous GitHub publisher accepted")
		}
	}
}
