package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func testRequest() Request {
	return Request{Project: "blog", Repository: "octavia/website", Ref: "refs/heads/main", Filename: "ozal.yml", Endpoint: "https://deploy.example.test", SiteURL: "https://blog.example.test", Directory: ".", Output: "dist"}
}

func TestBuildPinsToolsAndSeparatesBuildAuthority(t *testing.T) {
	p, err := Build(testRequest())
	if err != nil {
		t.Fatal(err)
	}
	build, deploy, ok := strings.Cut(p.Content, "  deploy:\n")
	if !ok || strings.Contains(build, "id-token:") || strings.Contains(deploy, "npm ") || strings.Contains(deploy, "actions/checkout@") {
		t.Fatal("repository code can run with the deployment credential")
	}
	for _, want := range []string{"permissions: {}", "persist-credentials: false", "id-token: write", "go install github.com/ozal-project/ozal/cmd/ozalctl@" + OperatorRevision, "site check -dir", "site pack -dir", "site deploy -archive", "site accept -url", "steps.deploy.outputs.live == 'true'", "cancel-in-progress: false", "if: github.ref == 'refs/heads/main'"} {
		if !strings.Contains(p.Content, want) {
			t.Errorf("missing security contract: %s", want)
		}
	}
	for _, line := range strings.Split(p.Content, "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		_, ref, _ := strings.Cut(line, "@")
		sha, _, _ := strings.Cut(ref, " ")
		if b, err := hex.DecodeString(sha); err != nil || len(b) != 20 {
			t.Errorf("action is not pinned: %s", line)
		}
	}
	for _, forbidden := range []string{"pull_request_target", "workflow_run:", "self-hosted", "write-all", "@main", "secrets.", "set -x"} {
		if strings.Contains(p.Content, forbidden) {
			t.Errorf("unsafe generated workflow: %s", forbidden)
		}
	}
	// A cache outage after successful acceptance must not redeploy a site or
	// claim that public acceptance failed. Only a live deploy/accept failure
	// can enter the recovery step.
	if !strings.Contains(p.Content, "id: acceptance\n") || !strings.Contains(p.Content, "if: failure() && steps.deploy.outputs.live == 'true' && (steps.deploy.outcome == 'failure' || steps.acceptance.outcome == 'failure')") {
		t.Fatal("rollback is not limited to a failed live deployment or acceptance")
	}
	sum := sha256.Sum256([]byte(p.Content))
	if p.Digest != hex.EncodeToString(sum[:]) || p.Path != ".github/workflows/ozal.yml" || p.Base != "main" || p.Branch != "ozal/publish-blog-"+p.Digest[:16] {
		t.Fatalf("plan does not bind the reviewed content: %+v", p)
	}
	other, _ := Build(testRequest())
	if other != p {
		t.Fatal("a retry gets another branch")
	}
}

func TestBuildRejectsInjectionTraversalAndNonBranchTrust(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Request)
	}{
		{"project", func(r *Request) { r.Project = "blog\npermissions: write-all" }},
		{"repository path", func(r *Request) { r.Repository = "../website" }},
		{"filename path", func(r *Request) { r.Filename = "../deploy.yml" }},
		{"filename expression", func(r *Request) { r.Filename = "${{github.ref}}.yml" }},
		{"tag", func(r *Request) { r.Ref = "refs/tags/main" }},
		{"ref injection", func(r *Request) { r.Ref = "refs/heads/main' || true" }},
		{"ref traversal", func(r *Request) { r.Ref = "refs/heads/../main" }},
		{"ref hidden", func(r *Request) { r.Ref = "refs/heads/.hidden/main" }},
		{"ref suffix", func(r *Request) { r.Ref = "refs/heads/main.lock" }},
		{"directory traversal", func(r *Request) { r.Directory = "../web" }},
		{"output traversal", func(r *Request) { r.Output = "build/../../private" }},
		{"output absolute", func(r *Request) { r.Output = "/tmp/dist" }},
		{"output option", func(r *Request) { r.Output = "--help" }},
		{"output expression", func(r *Request) { r.Output = "${{ secrets.TOKEN }}" }},
		{"output shell", func(r *Request) { r.Output = "$(touch /tmp/unsafe)" }},
		{"endpoint plain", func(r *Request) { r.Endpoint = "http://deploy.example.test" }},
		{"endpoint userinfo", func(r *Request) { r.Endpoint = "https://user:pass@deploy.example.test" }},
		{"endpoint path", func(r *Request) { r.Endpoint = "https://deploy.example.test/subpath" }},
		{"endpoint expression", func(r *Request) { r.Endpoint = "https://deploy.example.test/${{ secrets.TOKEN }}" }},
		{"site query", func(r *Request) { r.SiteURL = "https://blog.example.test/?token=x" }},
		{"oversized origin", func(r *Request) { r.SiteURL = "https://" + strings.Repeat("x", 513) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRequest()
			tc.change(&r)
			if _, err := Build(r); err == nil {
				t.Fatal("unsafe workflow accepted")
			}
		})
	}
}

func TestBuildSupportsMonorepoAndBranchNamesWithSlashes(t *testing.T) {
	r := testRequest()
	r.Directory, r.Output, r.Ref = "web/site", "build/client", "refs/heads/release/website"
	p, err := Build(r)
	if err != nil || !strings.Contains(p.Content, `path: "web/site/build/client"`) || !strings.Contains(p.Content, `working-directory: "web/site"`) || p.Base != "release/website" {
		t.Fatalf("monorepo plan: %v %v", p, err)
	}
	r.Output = "build/other"
	other, _ := Build(r)
	if other.Branch == p.Branch {
		t.Fatal("different content reused the reviewed branch")
	}
}

func FuzzBuild(f *testing.F) {
	f.Add("main", ".", "dist", "https://blog.example.test")
	f.Add("${{ secrets.TOKEN }}", "../", "$(false)", "https://example.test")
	f.Fuzz(func(t *testing.T, ref, directory, output, address string) {
		r := testRequest()
		r.Ref, r.Directory, r.Output, r.SiteURL = "refs/heads/"+ref, directory, output, address
		p, err := Build(r)
		if err != nil {
			return
		}
		if len(p.Content) > 20000 || !validPath(p.Directory, true) || !validPath(p.Output, false) || strings.Contains(p.SiteURL, "${{") {
			t.Fatal("unbounded or unsafe workflow")
		}
	})
}
