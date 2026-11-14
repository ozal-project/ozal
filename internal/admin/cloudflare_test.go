package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
)

const goodToken = "abcdefghijklmnopqrstuvwxyz0123456789ABCD"

func cloudflareHandler(t *testing.T, verify func(context.Context, string) ([]string, error)) (*Handler, *fakeManager, string) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(`{"listen": ":443", "admin": {"token": "secret"},
		"tls": {"enabled": true, "cache_dir": "/var/lib/ozal/acme", "email": "ops@example.com", "domains": ["auth.lab.example.org"]},
		"auth": {"passkeys": {"rp_id": "lab.example.org", "login_url": "https://auth.lab.example.org"}},
		"routes": [{"host": "app.lab.example.org", "target": "http://127.0.0.1:3000"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeManager{cfg: *cfg}
	h := New(manager, &events.Recorder{}, WithCloudflareSetup(CloudflareSetup{
		Dir:    dir,
		Verify: verify,
		Command: func(file, target string) string {
			return "sudo ozal cloudflare enable -token-file " + file + " -target " + target
		},
		Lookup: func(context.Context, string) ([]string, error) { return []string{"10.0.0.2", "203.0.113.10"}, nil },
	}))
	return h, manager, dir
}

// The token is checked with Cloudflare, kept in a 0600 file beside the state,
// never returned, and turned into the host command that enables it.
func TestCloudflareTokenIsVerifiedKeptPrivateAndNeverShown(t *testing.T) {
	h, _, dir := cloudflareHandler(t, func(_ context.Context, token string) ([]string, error) {
		if token != goodToken {
			return nil, errors.New("API error 400: Invalid API Token")
		}
		return []string{"example.org"}, nil
	})
	status := decode(t, request(h, http.MethodGet, "/_/api/cloudflare", "secret", ""))
	if status["available"] != true || status["saved"] != false || status["suggested_target"] != "203.0.113.10" {
		t.Fatalf("status = %v", status)
	}

	for body, want := range map[string]string{
		`{"token":"not a token","target":"203.0.113.10"}`:                            "does not look like",
		`{"token":"` + goodToken + `","target":"192.168.1.5"}`:                       "not a public address",
		`{"token":"` + strings.ToLower(goodToken) + `","target":"edge.example.org"}`: "it is not valid",
	} {
		rec := request(h, http.MethodPost, "/_/api/cloudflare/token", "secret", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s = %d %s", body, rec.Code, rec.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, cloudflareTokenFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused token was stored: %v", err)
	}

	rec := request(h, http.MethodPost, "/_/api/cloudflare/token", "secret", `{"token":"`+goodToken+`","target":"203.0.113.10"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), goodToken) {
		t.Fatalf("save = %d %s", rec.Code, rec.Body)
	}
	saved := decode(t, rec)
	command, _ := saved["command"].(string)
	if saved["saved"] != true || !strings.Contains(command, filepath.Join(dir, cloudflareTokenFile)) || !strings.Contains(command, "203.0.113.10") {
		t.Fatalf("saved = %v", saved)
	}
	// The plan to review: one zone for the sign-in host and the app.
	plan, _ := saved["plan"].(map[string]any)
	if plan == nil || len(plan["zones"].([]any)) != 1 || plan["zones"].([]any)[0] != "lab.example.org" {
		t.Fatalf("plan = %v (%v)", saved["plan"], saved["plan_error"])
	}
	info, err := os.Stat(filepath.Join(dir, cloudflareTokenFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", info, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, cloudflareTokenFile)); strings.TrimSpace(string(data)) != goodToken {
		t.Fatal("the stored token is not the one given")
	}
	if body := request(h, http.MethodGet, "/_/api/cloudflare", "secret", "").Body.String(); strings.Contains(body, goodToken) {
		t.Fatal("the status showed the token")
	}
}

// Only a session with the applications tool may connect Cloudflare.
func TestCloudflareNeedsTheApplicationsTool(t *testing.T) {
	h, _, _ := cloudflareHandler(t, func(context.Context, string) ([]string, error) { return []string{"example.org"}, nil })
	people, _, err := h.operators.issue("People operator", false, "people")
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range []*httptest.ResponseRecorder{
		request(h, http.MethodGet, "/_/api/cloudflare", people, ""),
		request(h, http.MethodPost, "/_/api/cloudflare/token", people, `{"token":"`+goodToken+`","target":"203.0.113.10"}`),
		request(h, http.MethodDelete, "/_/api/cloudflare/token", people, ""),
	} {
		if rec.Code != http.StatusForbidden {
			t.Fatalf("without the tool = %d", rec.Code)
		}
	}
}

// A token the edge already uses for its certificates cannot be forgotten
// from the console; one that is only saved can.
func TestCloudflareTokenInUseIsNotForgotten(t *testing.T) {
	h, manager, dir := cloudflareHandler(t, func(context.Context, string) ([]string, error) { return []string{"example.org"}, nil })
	request(h, http.MethodPost, "/_/api/cloudflare/token", "secret", `{"token":"`+goodToken+`","target":"203.0.113.10"}`)
	manager.mu.Lock()
	manager.cfg.TLS.DNS01 = &config.DNS01{DNSProvider: "cloudflare", DNSToken: "file:" + filepath.Join(dir, cloudflareTokenFile)}
	manager.cfg.DNS = &config.DNS{Target: "203.0.113.10", Cloudflare: &config.DNSCloudflare{Token: "file:x"}}
	manager.mu.Unlock()
	if rec := request(h, http.MethodDelete, "/_/api/cloudflare/token", "secret", ""); rec.Code != http.StatusConflict {
		t.Fatalf("forget in use = %d", rec.Code)
	}
	status := map[string]any{}
	_ = json.Unmarshal(request(h, http.MethodGet, "/_/api/cloudflare", "secret", "").Body.Bytes(), &status)
	if status["active"] != true || status["command"] != nil {
		t.Fatalf("active status = %v", status)
	}
	manager.mu.Lock()
	manager.cfg.TLS.DNS01, manager.cfg.DNS = nil, nil
	manager.mu.Unlock()
	if rec := request(h, http.MethodDelete, "/_/api/cloudflare/token", "secret", ""); rec.Code != http.StatusOK {
		t.Fatalf("forget = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, cloudflareTokenFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the token stayed")
	}
}

func TestCloudflareTokenInUseCannotBeReplacedFromTheConsole(t *testing.T) {
	checks := 0
	h, manager, dir := cloudflareHandler(t, func(context.Context, string) ([]string, error) {
		checks++
		return []string{"example.org"}, nil
	})
	if rec := request(h, http.MethodPost, "/_/api/cloudflare/token", "secret", `{"token":"`+goodToken+`","target":"203.0.113.10"}`); rec.Code != http.StatusOK {
		t.Fatal("initial save failed")
	}
	manager.mu.Lock()
	manager.cfg.TLS.DNS01 = &config.DNS01{DNSProvider: "cloudflare", DNSToken: "file:" + filepath.Join(dir, cloudflareTokenFile)}
	manager.cfg.DNS = &config.DNS{Target: "203.0.113.10", Cloudflare: &config.DNSCloudflare{Token: "file:" + filepath.Join(dir, cloudflareTokenFile)}}
	manager.mu.Unlock()
	rec := request(h, http.MethodPost, "/_/api/cloudflare/token", "secret", `{"token":"`+strings.Repeat("X", 40)+`","target":"203.0.113.20"}`)
	if rec.Code != http.StatusConflict || checks != 1 {
		t.Fatalf("replacing an active token: status=%d checks=%d", rec.Code, checks)
	}
	stored, err := os.ReadFile(filepath.Join(dir, cloudflareTokenFile))
	if err != nil || strings.TrimSpace(string(stored)) != goodToken {
		t.Fatal("a refused replacement changed the active token")
	}
}

func TestCloudflareTokenUsedByOnlyCertificatesOrDNSCannotBeRemovedOrReplaced(t *testing.T) {
	for _, usage := range []string{"certificates", "records"} {
		t.Run(usage, func(t *testing.T) {
			h, manager, dir := cloudflareHandler(t, func(context.Context, string) ([]string, error) { return []string{"example.org"}, nil })
			if rec := request(h, http.MethodPost, "/_/api/cloudflare/token", "secret", `{"token":"`+goodToken+`","target":"203.0.113.10"}`); rec.Code != http.StatusOK {
				t.Fatal("initial save failed")
			}
			ref := "file:" + filepath.Join(dir, cloudflareTokenFile)
			manager.mu.Lock()
			if usage == "certificates" {
				manager.cfg.TLS.DNS01 = &config.DNS01{DNSProvider: "cloudflare", DNSToken: ref}
			} else {
				manager.cfg.DNS = &config.DNS{Cloudflare: &config.DNSCloudflare{Token: ref}}
			}
			manager.mu.Unlock()
			for _, method := range []string{http.MethodPost, http.MethodDelete} {
				rec := request(h, method, "/_/api/cloudflare/token", "secret", `{"token":"`+strings.Repeat("X", 40)+`","target":"203.0.113.20"}`)
				if rec.Code != http.StatusConflict {
					t.Fatalf("%s allowed changing a token used by %s: %d", method, usage, rec.Code)
				}
			}
			stored, err := os.ReadFile(filepath.Join(dir, cloudflareTokenFile))
			if err != nil || strings.TrimSpace(string(stored)) != goodToken {
				t.Fatal("the active token changed")
			}
		})
	}
}
