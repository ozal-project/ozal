package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func testKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func TestManifestAsksOnlyForWorkflowProposalPermissions(t *testing.T) {
	target, manifest, err := Manifest(ManifestRequest{Name: "Ozal abc123", Origin: "https://console.example.com", State: "s t"})
	if err != nil {
		t.Fatal(err)
	}
	if target != "https://github.com/settings/apps/new?state=s+t" {
		t.Fatalf("target: %s", target)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(manifest), &m); err != nil {
		t.Fatal(err)
	}
	perms, _ := json.Marshal(m["default_permissions"])
	if string(perms) != `{"contents":"write","metadata":"read","pull_requests":"write","workflows":"write"}` {
		t.Fatalf("permissions: %s", perms)
	}
	hook, _ := json.Marshal(m["hook_attributes"])
	if m["public"] != false || !strings.Contains(string(hook), `"active":false`) || len(m["default_events"].([]any)) != 0 {
		t.Fatal("App must be private, without webhooks or events")
	}
	if m["redirect_url"] != "https://console.example.com/applications" || m["setup_url"] != "https://console.example.com/applications" {
		t.Fatal("GitHub must return the browser to the console")
	}
	org, _, err := Manifest(ManifestRequest{Name: "Ozal", Origin: "http://127.0.0.1:8899", Organization: "acme-labs", State: "x"})
	if err != nil || org != "https://github.com/organizations/acme-labs/settings/apps/new?state=x" {
		t.Fatalf("organization target: %s %v", org, err)
	}
	for _, bad := range []ManifestRequest{
		{Name: "Ozal", Origin: "https://console.example.com/path", State: "x"},
		{Name: "Ozal", Origin: "javascript:alert(1)", State: "x"},
		{Name: "Ozal", Origin: "https://user@console.example.com", State: "x"},
		{Name: "Ozal", Origin: "https://console.example.com?next=evil", State: "x"},
		{Name: "Ozal", Origin: "https://console.example.com", Organization: "../evil", State: "x"},
		{Name: "Ozal", Origin: "https://console.example.com"},
	} {
		if _, _, err := Manifest(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestConvertManifestKeepsOnlyTheKey(t *testing.T) {
	key := testKeyPEM(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app-manifests/abc123/conversions" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected conversion request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		body, _ := json.Marshal(map[string]any{"id": 77, "slug": "ozal-abc123", "owner": map[string]any{"login": "octo"}, "pem": key, "client_secret": "oauth-secret", "webhook_secret": "hook-secret"})
		w.WriteHeader(201)
		w.Write(body)
	}))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	created, err := client.ConvertManifest(context.Background(), "abc123")
	if err != nil || created.ID != 77 || created.Slug != "ozal-abc123" || created.Owner != "octo" || !strings.Contains(string(created.Key), "PRIVATE KEY") {
		t.Fatalf("conversion: %+v %v", created, err)
	}
	raw, _ := json.Marshal(created)
	if strings.Contains(string(raw), "oauth-secret") || strings.Contains(string(raw), "hook-secret") {
		t.Fatal("kept a secret Ozal does not use")
	}
	if _, err := client.ConvertManifest(context.Background(), "../app/installations"); err != ErrUnavailable {
		t.Fatal("code reached the API path unvalidated")
	}
}

func TestAppStatusFindsTheSingleInstallation(t *testing.T) {
	t.Setenv("TEST_GITHUB_SETUP_KEY", testKeyPEM(t))
	installations := `[]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			t.Error("App endpoints need the App JWT")
		}
		switch r.URL.Path {
		case "/app":
			w.Write([]byte(`{"id":7,"slug":"ozal-abc123","owner":{"login":"octo"}}`))
		case "/app/installations":
			w.Write([]byte(installations))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	cfg := config.GitHubApp{AppID: 7, PrivateKey: "env:TEST_GITHUB_SETUP_KEY"}
	status, err := client.AppStatus(context.Background(), cfg)
	if err != nil || status.Installation != 0 || status.InstallURL() != "https://github.com/apps/ozal-abc123/installations/new" {
		t.Fatalf("not installed: %+v %v", status, err)
	}
	installations = `[{"id":42}]`
	if status, err = client.AppStatus(context.Background(), cfg); err != nil || status.Installation != 42 {
		t.Fatalf("installed: %+v %v", status, err)
	}
	installations = `[{"id":42},{"id":43}]`
	if _, err = client.AppStatus(context.Background(), cfg); err != ErrUnavailable {
		t.Fatal("chose between two installations")
	}
	if _, err = client.AppStatus(context.Background(), config.GitHubApp{AppID: 8, PrivateKey: "env:TEST_GITHUB_SETUP_KEY"}); err != ErrUnavailable {
		t.Fatal("accepted another App's metadata")
	}
}

func TestUninstalledAppGetsNoInstallationToken(t *testing.T) {
	t.Setenv("TEST_GITHUB_SETUP_KEY", testKeyPEM(t))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("requested %s before installation", r.URL.Path)
	}))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	if _, err := client.Repositories(context.Background(), config.GitHubApp{AppID: 7, PrivateKey: "env:TEST_GITHUB_SETUP_KEY"}, 1); err != ErrUnavailable {
		t.Fatal("listed repositories without an installation")
	}
}
