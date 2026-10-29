package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func TestProjectTrustCLIBackupAndReconciliation(t *testing.T) {
	old := config.ProjectGitHubTrust{RepositoryID: 1, RepositoryOwnerID: 2, Ref: "refs/heads/main", WorkflowRef: "old/repo/.github/workflows/site.yml@refs/heads/main"}
	next := config.ProjectGitHubTrust{RepositoryID: 3, RepositoryOwnerID: 4, Ref: old.Ref, WorkflowRef: "new/repo/.github/workflows/site.yml@refs/heads/main"}
	for _, scenario := range []string{"plan", "success", "existing backup", "symlink backup", "stale", "wrong live trust", "already migrated"} {
		t.Run(scenario, func(t *testing.T) {
			var writes atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/_/api/setup":
					_ = json.NewEncoder(w).Encode(map[string]any{"config_sha256": "reviewed"})
				case "/_/api/projects/site/github-trust":
					var body struct {
						Expected string `json:"expected"`
						Apply    bool   `json:"apply"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Expected != "reviewed" {
						t.Error("migration did not retain the reviewed digest")
					}
					if body.Apply && scenario == "stale" {
						w.WriteHeader(409)
						_, _ = io.WriteString(w, `{"error":"configuration changed"}`)
						return
					}
					if body.Apply {
						writes.Add(1)
					}
					_ = json.NewEncoder(w).Encode(projectTrustResult{Name: "site", Before: old, After: next, Changed: scenario != "already migrated", Applied: body.Apply})
				case "/_/api/projects":
					live := next
					if scenario == "wrong live trust" {
						live = old
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"projects": []any{map[string]any{"name": "site", "deployers": []any{map[string]any{"github": live}}}}})
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			client := &adminClient{client: server.Client(), base: server.URL}
			backup := filepath.Join(t.TempDir(), "before.json")
			if scenario == "existing backup" {
				if err := os.WriteFile(backup, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink backup" {
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, backup); err != nil {
					t.Skip(err)
				}
			}
			var out bytes.Buffer
			err := migrateProjectTrust(context.Background(), &out, client, "site", next, scenario != "plan", backup)
			wantError := scenario == "existing backup" || scenario == "symlink backup" || scenario == "stale" || scenario == "wrong live trust"
			if (err != nil) != wantError {
				t.Fatalf("error=%v output=%s", err, out.String())
			}
			wantWrites := 0
			if scenario == "success" || scenario == "wrong live trust" {
				wantWrites = 1
			}
			if int(writes.Load()) != wantWrites {
				t.Fatalf("writes=%d want=%d", writes.Load(), wantWrites)
			}
			if scenario == "plan" || scenario == "already migrated" {
				if _, err := os.Stat(backup); !os.IsNotExist(err) {
					t.Fatal("plan/no-op wrote a backup")
				}
			} else if !strings.Contains(scenario, "backup") {
				data, err := os.ReadFile(backup)
				if err != nil {
					t.Fatal(err)
				}
				var kept struct {
					Before config.ProjectGitHubTrust `json:"before"`
				}
				if json.Unmarshal(data, &kept) != nil || kept.Before != old {
					t.Fatal("previous trust not kept")
				}
				if info, _ := os.Stat(backup); runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
					t.Fatal("backup is not private")
				}
			} else {
				data, _ := os.ReadFile(backup)
				if string(data) != "keep" {
					t.Fatal("existing backup target changed")
				}
			}
		})
	}
}

func TestProjectTrustCLIRefusesTCPBeforeAccess(t *testing.T) {
	err := runProject(context.Background(), []string{"trust", "-project", "site", "-repository-id", "1", "-owner-id", "2", "-workflow-ref", "a/b/.github/workflows/site.yml@refs/heads/main", "-admin", "http://127.0.0.1:1"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Unix socket") {
		t.Fatalf("TCP accepted: %v", err)
	}
}
