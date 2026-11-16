package routemanager

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/pipeline"
)

// admin.remote reaches the console through the same trust stage as every
// public request: the console sees the visitor's scheme and address as
// decided from the trusted peer, and other hosts are unaffected.
func TestRemoteConsoleHostGoesThroughTheTrustStage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "application")
	}))
	defer upstream.Close()
	console := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _ := pipeline.ClientIP(r.Context())
		fmt.Fprintf(w, "console https=%v ip=%s", pipeline.PublicHTTPS(r), ip)
	})
	cfg := config.Config{
		Listen:  "127.0.0.1:8888",
		Ingress: &config.Ingress{Mode: config.IngressCloudflareTunnel, CloudflareTunnel: &config.CloudflareTunnelIngress{Token: "env:OZAL_TEST_CF_TOKEN"}},
		Auth:    config.Auth{Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com"}},
		Admin:   config.Admin{LoginHost: true, Remote: &config.AdminRemote{Host: "console.example.com"}},
		Routes:  []config.Route{{Host: "app.example.com", Target: upstream.URL}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := writeConfig(t, cfg)
	m, err := New(ctx, path, time.Hour, WithRemoteConsole(console))
	if err != nil {
		t.Fatal(err)
	}
	serve := func(host, peer string) string {
		r := httptest.NewRequest("GET", "http://"+host+"/", nil)
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("CF-Connecting-IP", "198.51.100.23")
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return fmt.Sprintf("%d %s", w.Code, w.Body.String())
	}
	if got := serve("console.example.com", "127.0.0.1:40000"); got != "200 console https=true ip=198.51.100.23" {
		t.Fatalf("through cloudflared: %s", got)
	}
	if got := serve("Console.Example.com:443", "127.0.0.1:40000"); got != "200 console https=true ip=198.51.100.23" {
		t.Fatalf("with a port and another case: %s", got)
	}
	if got := serve("console.example.com", "192.0.2.50:40000"); got != "200 console https=false ip=192.0.2.50" {
		t.Fatalf("from an untrusted peer: %s", got)
	}
	if got := serve("app.example.com", "127.0.0.1:40000"); got != "200 application" {
		t.Fatalf("an application host: %s", got)
	}

	// The kill switch: without admin.remote the name is as unknown as any.
	cfg.Admin.Remote = nil
	overwriteConfig(t, path, cfg)
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := serve("console.example.com", "127.0.0.1:40000"); got == "200 console https=true ip=198.51.100.23" {
		t.Fatal("the remote console still answers after admin.remote was removed")
	}
}
