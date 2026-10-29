package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/pages/workflow"
)

func TestWorkflowPRWritesOnlyANewBranchAndOneNewFile(t *testing.T) {
	for _, failure := range []string{"", "token denied", "repo removed", "owner changed", "repo changed", "base missing", "workflow exists", "branch exists", "branch failed", "contents failed", "PR failed", "foreign PR URL", "redirect"} {
		t.Run(failure, func(t *testing.T) {
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("TEST_WORKFLOW_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})))
			cfg := config.GitHubApp{AppID: 7, InstallationID: 42, PrivateKey: "env:TEST_WORKFLOW_KEY"}
			selected := Repository{ID: 123, FullName: "octavia/website"}
			selected.Owner.ID = 456
			plan, err := workflow.Build(workflow.Request{Project: "blog", Repository: selected.FullName, Ref: "refs/heads/release/site", Filename: "ozal.yml", Endpoint: "https://deploy.example.test", SiteURL: "https://blog.example.test", Directory: ".", Output: "dist"})
			if err != nil {
				t.Fatal(err)
			}
			var writes []string
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				if r.URL.Path != "/app/installations/42/access_tokens" && r.Header.Get("Authorization") != "Bearer ephemeral-test-credential" {
					t.Error("wrong credential")
				}
				if r.Method != "GET" && r.URL.Path != "/app/installations/42/access_tokens" {
					writes = append(writes, r.Method+" "+r.URL.Path)
				}
				switch r.Method + " " + r.URL.Path {
				case "POST /app/installations/42/access_tokens":
					var body struct {
						RepositoryIDs []int64           `json:"repository_ids"`
						Permissions   map[string]string `json:"permissions"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if len(body.RepositoryIDs) != 1 || body.RepositoryIDs[0] != 123 || len(body.Permissions) != 4 || body.Permissions["workflows"] != "write" {
						t.Error("write token is not repository scoped")
					}
					if failure == "token denied" {
						w.WriteHeader(403)
						w.Write([]byte("ephemeral-test-credential /private/path"))
						return
					}
					w.Write([]byte(`{"token":"ephemeral-test-credential"}`))
				case "GET /repositories/123":
					if failure == "redirect" {
						http.Redirect(w, r, "/unexpected-redirect", http.StatusFound)
						return
					}
					if failure == "repo removed" {
						w.WriteHeader(404)
						return
					}
					current := selected
					if failure == "owner changed" {
						current.Owner.ID++
					}
					if failure == "repo changed" {
						current.ID++
					}
					json.NewEncoder(w).Encode(current)
				case "GET /repos/octavia/website/git/ref/heads/release/site":
					if failure == "base missing" {
						w.WriteHeader(404)
						return
					}
					w.Write([]byte(`{"ref":"refs/heads/release/site","object":{"type":"commit","sha":"1111111111111111111111111111111111111111"}}`))
				case "GET /repos/octavia/website/contents/.github/workflows/ozal.yml":
					if r.URL.Query().Get("ref") != strings.Repeat("1", 40) {
						t.Error("absence check did not use the selected commit")
					}
					if failure == "workflow exists" {
						w.Write([]byte(`{"type":"file"}`))
						return
					}
					w.WriteHeader(404)
				case "GET /repos/octavia/website/git/ref/heads/" + plan.Branch:
					if failure == "branch exists" {
						w.Write([]byte(`{}`))
						return
					}
					w.WriteHeader(404)
				case "POST /repos/octavia/website/git/refs":
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if len(body) != 2 || body["ref"] != "refs/heads/"+plan.Branch || body["sha"] != strings.Repeat("1", 40) {
						t.Error("wrong branch or starting commit")
					}
					if failure == "branch failed" {
						w.WriteHeader(422)
						return
					}
					w.WriteHeader(201)
					w.Write([]byte(`{}`))
				case "PUT /repos/octavia/website/contents/.github/workflows/ozal.yml":
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					content, _ := base64.StdEncoding.DecodeString(body["content"])
					if len(body) != 3 || body["sha"] != "" || body["branch"] != plan.Branch || string(content) != plan.Content {
						t.Error("overwriting a file, changing base or writing unreviewed content")
					}
					if failure == "contents failed" {
						w.WriteHeader(500)
						return
					}
					w.WriteHeader(201)
					w.Write([]byte(`{}`))
				case "POST /repos/octavia/website/pulls":
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["head"] != plan.Branch || body["base"] != "release/site" || !strings.Contains(body["body"].(string), plan.Digest) {
						t.Error("PR does not match reviewed plan")
					}
					if failure == "PR failed" {
						w.WriteHeader(403)
						return
					}
					if failure == "foreign PR URL" {
						w.WriteHeader(201)
						w.Write([]byte(`{"number":19,"html_url":"https://evil.example.test/pull/19"}`))
						return
					}
					w.WriteHeader(201)
					w.Write([]byte(`{"number":19,"html_url":"https://github.com/octavia/website/pull/19"}`))
				default:
					t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			client := New()
			client.BaseURL = server.URL
			result, err := client.OpenWorkflow(context.Background(), cfg, selected, plan)
			if failure == "" {
				if err != nil || result.URL != "https://github.com/octavia/website/pull/19" || result.Branch != plan.Branch || len(writes) != 3 {
					t.Fatalf("PR result: %+v %v writes=%v", result, err, writes)
				}
			} else {
				if err == nil || strings.Contains(err.Error(), "ephemeral-test-credential") || strings.Contains(err.Error(), "/private/path") {
					t.Fatalf("failure not redacted: %v", err)
				}
				wantWrites := 0
				switch failure {
				case "branch failed":
					wantWrites = 1
				case "contents failed":
					wantWrites = 2
				case "PR failed", "foreign PR URL":
					wantWrites = 3
				}
				if len(writes) != wantWrites {
					t.Fatalf("writes after refusal: %v; calls=%v", writes, calls)
				}
				if wantWrites > 0 && (!errors.Is(err, ErrUnconfirmed) || result.Branch != plan.Branch) {
					t.Fatal("partial result did not preserve the branch for reconciliation")
				}
			}
		})
	}
}

func TestWorkflowPRRefusesUnreviewedPlanBeforeCredentials(t *testing.T) {
	plan, _ := workflow.Build(workflow.Request{Project: "blog", Repository: "octavia/website", Ref: "refs/heads/main", Filename: "ozal.yml", Endpoint: "https://deploy.example.test", SiteURL: "https://blog.example.test", Directory: ".", Output: "dist"})
	plan.Content += "\npermissions: write-all\n"
	if _, err := New().OpenWorkflow(context.Background(), config.GitHubApp{}, Repository{ID: 123}, plan); err == nil {
		t.Fatal("unreviewed workflow accepted")
	}
}
