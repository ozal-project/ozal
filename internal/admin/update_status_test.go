package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

func TestUpdateStatusAPIRequiresTheUpdatesToolAndReturnsOnlyTheProjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-status.json")
	// A fresh check: a fixed date would turn stale and fail as the calendar moves.
	checked := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(path, []byte(`{"checked_at":"`+checked+`","installed":"0.1.0","latest":"0.2.0","result":"available","reason":"private failure detail: token=DO_NOT_EXPOSE"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(path))
	updates, _, err := h.operators.issue("Updater", false, "updates")
	if err != nil {
		t.Fatal(err)
	}
	people, _, err := h.operators.issue("People", false, "people")
	if err != nil {
		t.Fatal(err)
	}
	if rec := request(h, http.MethodGet, "/_/api/updates/status", people, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("people-only session = %d, want 403", rec.Code)
	}
	rec := request(h, http.MethodGet, "/_/api/updates/status", updates, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"result":"available"`) || !strings.Contains(rec.Body.String(), `"latest":"0.2.0"`) {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("update status response can be cached")
	}
	if strings.Contains(rec.Body.String(), "DO_NOT_EXPOSE") {
		t.Fatal("private updater detail was returned by the console API")
	}
}

func TestUpdateStatusAPIReturnsUnknownBeforeFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-created.json")
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(path))
	rec := request(h, http.MethodGet, "/_/api/updates/status", readToken, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"result":"unknown"`) {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unknown update status response can be cached")
	}
}

func TestUpdateStatusAPIExpiresOldChecksAndInterruptedJobs(t *testing.T) {
	for _, test := range []struct {
		name   string
		age    time.Duration
		result string
		stale  bool
	}{
		{name: "daily check expired", age: 25 * time.Hour, result: "available", stale: true},
		{name: "interrupted job expired", age: 61 * time.Minute, result: "updating", stale: true},
		{name: "fresh check", age: time.Hour, result: "available"},
		{name: "terminal result is historical", age: 10 * 24 * time.Hour, result: "updated"},
		{name: "implausibly future timestamp", age: -10 * time.Minute, result: "current", stale: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "update-status.json")
			status := updateStatusView{
				CheckedAt: time.Now().Add(-test.age).UTC().Format(time.RFC3339),
				Installed: "0.1.0", Latest: "0.2.0", Result: test.result,
			}
			if test.result == "updating" {
				status.JobID, status.Phase = "pair-job", "verifying"
				status.Nodes = []updateNodeStatus{{Role: "edge", Name: "edge", Installed: "0.1.0", Latest: "0.2.0", Result: "staged"}, {Role: "connector", Name: "home", Installed: "0.1.0", Latest: "0.2.0", Result: "staged"}}
			}
			raw, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(path))
			rec := request(h, http.MethodGet, "/_/api/updates/status", readToken, "")
			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d %s", rec.Code, body)
			}
			if test.stale {
				if !strings.Contains(body, `"stale":true`) || !strings.Contains(body, `"result":"unknown"`) || !strings.Contains(body, `"last_result":"`+test.result+`"`) {
					t.Fatalf("expired status was not made explicit: %s", body)
				}
				for _, private := range []string{`"latest"`, `"job_id"`, `"phase"`, `"nodes"`} {
					if strings.Contains(body, private) {
						t.Fatalf("expired detail %s remained in response: %s", private, body)
					}
				}
			} else if strings.Contains(body, `"stale":true`) || !strings.Contains(body, `"result":"`+test.result+`"`) {
				t.Fatalf("fresh/terminal status was changed: %s", body)
			}
		})
	}
}

func TestUpdateStatusAPIRefusesMalformedOrSymlinkedFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"result":"root-command-accepted"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(bad))
	if rec := request(h, http.MethodGet, "/_/api/updates/status", readToken, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unknown result = %d, want 503", rec.Code)
	}
	link := filepath.Join(dir, "status-link.json")
	if err := os.Symlink(bad, link); err != nil {
		t.Fatal(err)
	}
	h = New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(link))
	if rec := request(h, http.MethodGet, "/_/api/updates/status", readToken, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("symlink status = %d, want 503", rec.Code)
	}
}

func TestUpdateStatusReportAcceptsOnlyTheHostSocketAndPersistsBoundedPairState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-status.json")
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(path))
	body, err := json.Marshal(map[string]any{
		"checked_at": time.Now().UTC().Format(time.RFC3339), "installed": "0.1.0", "latest": "0.2.0", "result": "updating",
		"job_id": "pair-123", "phase": "installing", "reason": "must be replaced", "stale": true, "last_result": "must-not-be-trusted",
		"nodes": []map[string]string{
			{"role": "edge", "name": "edge", "installed": "0.1.0", "latest": "0.2.0", "sha256": strings.Repeat("a", 64), "result": "staged"},
			{"role": "connector", "name": "home", "installed": "0.1.0", "latest": "0.2.0", "sha256": strings.Repeat("b", 64), "result": "staged"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := request(h, http.MethodPost, "/_/api/updates/status", readToken, string(body))
	if remote.Code != http.StatusForbidden {
		t.Fatalf("remote report = %d, want 403", remote.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/_/api/updates/status", strings.NewReader(string(body)))
	req.Host = "127.0.0.1:9090"
	req = req.WithContext(context.WithValue(req.Context(), localKey{}, true))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || strings.Contains(rec.Body.String(), "must be replaced") || !strings.Contains(rec.Body.String(), `"result":"updating"`) {
		t.Fatalf("local report = %d %s", rec.Code, rec.Body.String())
	}
	got := request(h, http.MethodGet, "/_/api/updates/status", readToken, "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"role":"connector"`) || strings.Contains(got.Body.String(), "must be replaced") {
		t.Fatalf("read pair status = %d %s", got.Code, got.Body.String())
	}
}

func TestUpdateStatusReportRejectsUnknownFieldsAndInvalidRoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-status.json")
	h := New(&fakeManager{}, nil, WithUpdateStatusFile(path))
	for _, body := range []string{
		`{"checked_at":"2026-10-30T04:00:00Z","result":"updating","command":"systemctl reboot"}`,
		`{"checked_at":"2026-10-30T04:00:00Z","result":"updating","nodes":[{"role":"host","name":"edge","result":"pending"}]}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/_/api/updates/status", strings.NewReader(body))
		req.Host = "127.0.0.1:9090"
		req = req.WithContext(context.WithValue(req.Context(), localKey{}, true))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s = %d, want 400", body, rec.Code)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid status modified persisted state: %v", err)
	}
}
