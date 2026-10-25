package deployauth

import (
	"strings"
	"testing"
	"time"
)

func TestDecideDeployer(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	workflow := "owner/repo/.github/workflows/deploy.yml@refs/heads/main"
	trust := Trust{RepositoryID: 123, RepositoryOwnerID: 42, Ref: "refs/heads/main", WorkflowRef: workflow, Audience: "https://deploy.example"}
	valid := Claims{RepositoryID: 123, RepositoryOwnerID: 42, Ref: trust.Ref, WorkflowRef: workflow, EventName: "push", Audience: []string{trust.Audience},
		JWTID: "unique-token", CommitSHA: strings.Repeat("a", 40), IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(4 * time.Minute), ObservedAt: now}
	for _, tc := range []struct {
		name, code string
		change     func(*Claims, *Trust)
	}{
		{"valid", "deployer.allowed", func(*Claims, *Trust) {}},
		{"other repository", "deployer.repository", func(c *Claims, _ *Trust) { c.RepositoryID++ }},
		{"other owner", "deployer.owner", func(c *Claims, _ *Trust) { c.RepositoryOwnerID++ }},
		{"other branch", "deployer.ref", func(c *Claims, _ *Trust) { c.Ref = "refs/heads/other" }},
		{"wrong audience", "deployer.audience", func(c *Claims, _ *Trust) { c.Audience = []string{"other"} }},
		{"expired", "deployer.expired", func(c *Claims, _ *Trust) { c.ExpiresAt = now }},
		{"long lifetime", "deployer.lifetime", func(c *Claims, _ *Trust) { c.ExpiresAt = now.Add(time.Hour) }},
		{"future issue time", "deployer.lifetime", func(c *Claims, _ *Trust) { c.IssuedAt = now.Add(time.Minute) }},
		{"missing jti", "deployer.claims", func(c *Claims, _ *Trust) { c.JWTID = "" }},
		{"invalid commit", "deployer.claims", func(c *Claims, _ *Trust) { c.CommitSHA = "untrusted\nvalue" }},
		{"other workflow", "deployer.workflow", func(c *Claims, _ *Trust) { c.WorkflowRef = "owner/repo/.github/workflows/other.yml@refs/heads/main" }},
		{"unpinned workflow", "deployer.workflow", func(_ *Claims, tr *Trust) { tr.WorkflowRef = "" }},
		{"manual run", "deployer.allowed", func(c *Claims, _ *Trust) { c.EventName = "workflow_dispatch" }},
		{"pull_request_target on the base branch", "deployer.event", func(c *Claims, _ *Trust) { c.EventName = "pull_request_target" }},
		{"pull request", "deployer.event", func(c *Claims, _ *Trust) { c.EventName = "pull_request" }},
		{"missing event", "deployer.event", func(c *Claims, _ *Trust) { c.EventName = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims, configured := valid, trust
			tc.change(&claims, &configured)
			got := DecideDeployer(claims, configured)
			if got.Code != tc.code || got.Allowed != (tc.code == "deployer.allowed") {
				t.Fatalf("decision = %+v, want %s", got, tc.code)
			}
		})
	}
}
