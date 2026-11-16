package certs

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTunnelAPI simulates the account tunnel endpoints Ozal uses.
type fakeTunnelAPI struct {
	t       *testing.T
	tunnels map[string]map[string]any // id -> tunnel body
	ingress map[string]any            // id -> config
	posts   int
}

func (f *fakeTunnelAPI) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-token" {
		f.t.Errorf("missing bearer token on %s %s", r.Method, r.URL.Path)
	}
	ok := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/zones":
		if r.URL.Query().Get("name") == "example.com" {
			ok([]any{map[string]any{"id": "zone-1", "name": "example.com", "account": map[string]any{"id": "acc-1"}}})
			return
		}
		ok([]any{})
	case r.Method == http.MethodGet && path == "/accounts/acc-1/cfd_tunnel":
		var out []any
		for _, t := range f.tunnels {
			if t["name"] == r.URL.Query().Get("name") {
				out = append(out, t)
			}
		}
		ok(out)
	case r.Method == http.MethodPost && path == "/accounts/acc-1/cfd_tunnel":
		f.posts++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["config_src"] != "cloudflare" {
			f.t.Errorf("tunnel not remotely managed: %v", body)
		}
		t := map[string]any{"id": "c0ffee00-0000-4000-8000-000000000001", "name": body["name"], "remote_config": true, "status": "inactive", "connections": []any{}}
		f.tunnels[t["id"].(string)] = t
		created := map[string]any{"token": "connector-secret"}
		for k, v := range t {
			created[k] = v
		}
		ok(created)
	case strings.HasSuffix(path, "/token") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/acc-1/cfd_tunnel/"), "/token")
		if f.tunnels[id] == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"success":false,"errors":[{"code":1003,"message":"not found"}]}`)
			return
		}
		ok("connector-secret")
	case strings.HasSuffix(path, "/configurations"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/acc-1/cfd_tunnel/"), "/configurations")
		if r.Method == http.MethodPut {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.ingress[id] = body["config"]
			ok(map[string]any{})
			return
		}
		ok(map[string]any{"config": f.ingress[id]})
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/accounts/acc-1/cfd_tunnel/"):
		t := f.tunnels[strings.TrimPrefix(path, "/accounts/acc-1/cfd_tunnel/")]
		if t == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"success":false,"errors":[{"code":1003,"message":"not found"}]}`)
			return
		}
		ok(t)
	default:
		f.t.Errorf("unexpected call %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func newTunnelClient(t *testing.T) (*CloudflareClient, *fakeTunnelAPI) {
	fake := &fakeTunnelAPI{t: t, tunnels: map[string]map[string]any{}, ingress: map[string]any{}}
	api := httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(api.Close)
	client, err := NewCloudflareClient(CloudflareConfig{Token: "test-token", BaseURL: api.URL, HTTPClient: api.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return client, fake
}

func TestCloudflareTunnelLifecycle(t *testing.T) {
	client, fake := newTunnelClient(t)
	ctx := t.Context()

	account, err := client.AccountForZone(ctx, "auth.example.com")
	if err != nil || account != "acc-1" {
		t.Fatalf("account = %q, %v", account, err)
	}
	if _, found, err := client.FindTunnel(ctx, account, "ozal"); err != nil || found {
		t.Fatalf("found a tunnel before creating one: %v %v", found, err)
	}
	created, token, err := client.CreateTunnel(ctx, account, "ozal")
	if err != nil || token != "connector-secret" || !created.Remote || created.ID == "" {
		t.Fatalf("create = %+v, token %q, %v", created, token, err)
	}
	found, ok, err := client.FindTunnel(ctx, account, "ozal")
	if err != nil || !ok || found.ID != created.ID {
		t.Fatalf("find after create = %+v %v %v", found, ok, err)
	}
	if again, err := client.TunnelToken(ctx, account, created.ID); err != nil || again != "connector-secret" {
		t.Fatalf("token = %q, %v", again, err)
	}
	rules := []TunnelIngressRule{{Service: "http://127.0.0.1:8888"}}
	if err := client.SetTunnelIngress(ctx, account, created.ID, rules); err != nil {
		t.Fatal(err)
	}
	read, err := client.TunnelIngress(ctx, account, created.ID)
	if err != nil || len(read) != 1 || read[0].Service != "http://127.0.0.1:8888" || read[0].Hostname != "" {
		t.Fatalf("ingress = %+v, %v", read, err)
	}
	status, err := client.Tunnel(ctx, account, created.ID)
	if err != nil || status.Status != "inactive" || status.Connections != 0 {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if fake.posts != 1 {
		t.Fatalf("created %d tunnels", fake.posts)
	}
}

func TestCloudflareTunnelMissingAndInvalidIngress(t *testing.T) {
	client, _ := newTunnelClient(t)
	ctx := t.Context()
	if _, err := client.Tunnel(ctx, "acc-1", "gone"); !errors.Is(err, ErrTunnelMissing) {
		t.Fatalf("missing tunnel error = %v", err)
	}
	if _, err := client.TunnelToken(ctx, "acc-1", "gone"); !errors.Is(err, ErrTunnelMissing) {
		t.Fatalf("missing token error = %v", err)
	}
	if err := client.SetTunnelIngress(ctx, "acc-1", "id", []TunnelIngressRule{{Hostname: "a.example.com", Service: "http://127.0.0.1:8888"}}); err == nil {
		t.Fatal("accepted an ingress without a catch-all")
	}
	if _, err := client.AccountForZone(ctx, "app.other.org"); err == nil {
		t.Fatal("found an account for a zone outside the token's reach")
	}
}
