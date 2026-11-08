package cftunnel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/cloudflare"
	"github.com/ozal-project/ozal/internal/config"
)

type fakeClient struct {
	tunnels     map[string]certs.CloudflareTunnel
	ingress     map[string][]certs.TunnelIngressRule
	creates     int
	loseCreate  bool
	tokenCalls  int
	accountZone string
}

func newFake() *fakeClient {
	return &fakeClient{tunnels: map[string]certs.CloudflareTunnel{}, ingress: map[string][]certs.TunnelIngressRule{}, accountZone: "example.com"}
}

func (f *fakeClient) AccountForZone(_ context.Context, name string) (string, error) {
	if strings.HasSuffix(name, f.accountZone) {
		return "acc-1", nil
	}
	return "", errors.New("no zone")
}
func (f *fakeClient) FindTunnel(_ context.Context, _, name string) (certs.CloudflareTunnel, bool, error) {
	for _, t := range f.tunnels {
		if t.Name == name {
			return t, true, nil
		}
	}
	return certs.CloudflareTunnel{}, false, nil
}
func (f *fakeClient) CreateTunnel(_ context.Context, _, name string) (certs.CloudflareTunnel, string, error) {
	f.creates++
	t := certs.CloudflareTunnel{ID: "c0ffee00-0000-4000-8000-00000000000" + string(rune('0'+f.creates)), Name: name, Remote: true, Status: "inactive"}
	f.tunnels[t.ID] = t
	if f.loseCreate {
		return certs.CloudflareTunnel{}, "", certs.ErrAmbiguous
	}
	return t, "token-from-create", nil
}
func (f *fakeClient) TunnelToken(_ context.Context, _, id string) (string, error) {
	f.tokenCalls++
	if _, ok := f.tunnels[id]; !ok {
		return "", certs.ErrTunnelMissing
	}
	return "token-fetched", nil
}
func (f *fakeClient) Tunnel(_ context.Context, _, id string) (certs.CloudflareTunnel, error) {
	t, ok := f.tunnels[id]
	if !ok {
		return certs.CloudflareTunnel{}, certs.ErrTunnelMissing
	}
	return t, nil
}
func (f *fakeClient) TunnelIngress(_ context.Context, _, id string) ([]certs.TunnelIngressRule, error) {
	return f.ingress[id], nil
}
func (f *fakeClient) SetTunnelIngress(_ context.Context, _, id string, rules []certs.TunnelIngressRule) error {
	f.ingress[id] = rules
	return nil
}

func tunnelConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "api.token")
	if err := os.WriteFile(tokenPath, []byte("api-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Config{
		Listen: "127.0.0.1:8888",
		Auth:   config.Auth{Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com"}},
		Ingress: &config.Ingress{Mode: config.IngressCloudflareTunnel, CloudflareTunnel: &config.CloudflareTunnelIngress{
			Token: "file:" + tokenPath, StateDir: filepath.Join(dir, "state"), Name: "ozal"}},
	}
}

func serviceWith(fake *fakeClient) *Service {
	return &Service{Cloudflare: func(token string) (Client, error) {
		if token != "api-token" {
			return nil, errors.New("wrong token")
		}
		return fake, nil
	}}
}

func TestApplyCreatesTunnelSavesTokenAndRoutesToThisEdge(t *testing.T) {
	cfg := tunnelConfig(t)
	fake := newFake()
	s := serviceWith(fake)

	before := s.Inspect(t.Context(), cfg)
	if before.Ready || before.Steps[0].Action != cloudflare.CreateTunnel || Target(cfg) != "" {
		t.Fatalf("before = %+v", before)
	}
	report, err := s.Apply(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fake.creates != 1 || strings.Join(report.Applied, ",") != "create_tunnel,set_ingress" {
		t.Fatalf("creates = %d, applied = %v", fake.creates, report.Applied)
	}
	state := cfg.Ingress.CloudflareTunnel.StateDir
	token, err := os.ReadFile(filepath.Join(state, TokenFile))
	if err != nil || string(token) != "token-from-create" {
		t.Fatalf("token file = %q, %v", token, err)
	}
	for _, name := range []string{TokenFile, recordFile} {
		info, err := os.Stat(filepath.Join(state, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, %v", name, info.Mode(), err)
		}
	}
	if dir, _ := os.Stat(state); dir.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode = %v", dir.Mode())
	}
	rules := fake.ingress[report.ID]
	if len(rules) != 1 || rules[0].Service != "http://127.0.0.1:8888" || rules[0].Hostname != "" {
		t.Fatalf("ingress = %+v", rules)
	}
	if Target(cfg) != report.ID+".cfargotunnel.com" || report.Target != Target(cfg) {
		t.Fatalf("target = %q, report %q", Target(cfg), report.Target)
	}
	if strings.Contains(strings.ToLower(report.Error+report.Steps[2].Detail), "token-from-create") {
		t.Fatal("the report exposed the connector token")
	}
	// The connector is the operator's step.
	if report.Steps[2].State != cloudflare.TunnelTodo || report.Steps[2].Manual != cloudflare.ConnectorCommand {
		t.Fatalf("connector step = %+v", report.Steps[2])
	}

	// A second apply changes nothing and creates no second tunnel.
	again, err := s.Apply(t.Context(), cfg)
	if err != nil || len(again.Applied) != 0 || fake.creates != 1 {
		t.Fatalf("second apply: applied %v, creates %d, %v", again.Applied, fake.creates, err)
	}

	// Once cloudflared connects, everything is ready.
	tunnel := fake.tunnels[report.ID]
	tunnel.Status, tunnel.Connections = "healthy", 4
	fake.tunnels[report.ID] = tunnel
	if ready := s.Inspect(t.Context(), cfg); !ready.Ready {
		t.Fatalf("not ready: %+v", ready.Steps)
	}
}

func TestApplyNeverCreatesASecondTunnel(t *testing.T) {
	t.Run("lost create answer", func(t *testing.T) {
		cfg := tunnelConfig(t)
		fake := newFake()
		fake.loseCreate = true
		report, err := serviceWith(fake).Apply(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if fake.creates != 1 || fake.tokenCalls != 1 || report.ID == "" {
			t.Fatalf("creates = %d, token calls = %d, report = %+v", fake.creates, fake.tokenCalls, report)
		}
	})
	t.Run("lost local state adopts the named tunnel", func(t *testing.T) {
		cfg := tunnelConfig(t)
		fake := newFake()
		fake.tunnels["existing"] = certs.CloudflareTunnel{ID: "existing", Name: "ozal", Remote: true, Status: "healthy", Connections: 2}
		report, err := serviceWith(fake).Apply(t.Context(), cfg)
		if err != nil || fake.creates != 0 || report.ID != "existing" {
			t.Fatalf("creates = %d, report = %+v, %v", fake.creates, report, err)
		}
	})
	t.Run("a locally managed tunnel with the name is refused", func(t *testing.T) {
		cfg := tunnelConfig(t)
		fake := newFake()
		fake.tunnels["local"] = certs.CloudflareTunnel{ID: "local", Name: "ozal", Remote: false}
		_, err := serviceWith(fake).Apply(t.Context(), cfg)
		if err == nil || !strings.Contains(err.Error(), "local cloudflared configuration") || fake.creates != 0 {
			t.Fatalf("err = %v, creates = %d", err, fake.creates)
		}
		if len(fake.ingress) != 0 {
			t.Fatal("changed the routing of a tunnel Ozal does not manage")
		}
	})
	t.Run("deleted in the dashboard is recreated", func(t *testing.T) {
		cfg := tunnelConfig(t)
		fake := newFake()
		s := serviceWith(fake)
		first, err := s.Apply(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		delete(fake.tunnels, first.ID)
		second, err := s.Apply(t.Context(), cfg)
		if err != nil || second.ID == first.ID || fake.creates != 2 {
			t.Fatalf("second = %+v, creates = %d, %v", second, fake.creates, err)
		}
	})
}

func TestInspectWithoutTheTunnelModeOrToken(t *testing.T) {
	direct := config.Config{Listen: "127.0.0.1:8888"}
	if got := New().Inspect(t.Context(), direct); got.Mode != config.IngressDirect || len(got.Steps) != 0 {
		t.Fatalf("direct = %+v", got)
	}
	cfg := tunnelConfig(t)
	cfg.Ingress.CloudflareTunnel.Token = "file:/nonexistent/token"
	report := serviceWith(newFake()).Inspect(t.Context(), cfg)
	if !strings.Contains(report.Error, "could not be read") || report.Ready {
		t.Fatalf("report = %+v", report)
	}
	if _, err := serviceWith(newFake()).Apply(t.Context(), cfg); err == nil {
		t.Fatal("applied without a readable token")
	}
}
