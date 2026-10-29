package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/tunnel"
)

func TestLabAddressAndRouteRestrictions(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8888", ":8888", "localhost:8888", "192.168.1.1:8888", "[::]:8888", "127.0.0.1:-1", "127.0.0.1:65536", "127.0.0.1:http"} {
		if err := validateLabAddress(addr, true); err == nil {
			t.Errorf("accepted non-lab address %q", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:0", "[::1]:8888"} {
		if err := validateLabAddress(addr, true); err != nil {
			t.Error(err)
		}
	}
	if err := validateLabAddress("127.0.0.1:0", false); err == nil {
		t.Fatal("accepted destination port zero")
	}
	for _, target := range []string{"http://example.com", "http://169.254.169.254:80", "http://localhost:8000", "https://127.0.0.1:8000", "http://user:secret@127.0.0.1:8000", "http://127.0.0.1:8000/path", "http://127.0.0.1:8000?query=1"} {
		if _, err := labRoute("portfolio.localhost", "", target); err == nil {
			t.Errorf("accepted non-loopback origin %q", target)
		}
	}
	for _, target := range []string{"http://127.0.0.1:8000", "http://[::1]:8000"} {
		if _, err := labRoute("portfolio.localhost", "", target); err != nil {
			t.Error(err)
		}
	}
	if _, err := labRoute("portfolio.localhost", "relative", ""); err == nil {
		t.Fatal("relative root accepted")
	}
	if _, err := labRoute("portfolio.localhost", "/tmp", "http://127.0.0.1:8000"); err == nil {
		t.Fatal("ambiguous route accepted")
	}
}

func TestTunnelSpikeInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"init"}, {"init", "-pki", filepath.Join(t.TempDir(), "pki"), "unexpected"}} {
		if err := runTunnelSpike(context.Background(), args, io.Discard); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestTunnelSpikeInitCreatesPrivateDisposableIdentities(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pki")
	var out bytes.Buffer
	if err := runTunnelSpike(context.Background(), []string{"init", "-pki", dir}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "PRIVATE KEY") {
		t.Fatal("key leaked")
	}
	for _, name := range []string{"edge.crt", "edge.key", "connector.crt", "connector.key", "ca.crt"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if hasUnixPermissions() && info.Mode().Perm() != 0600 {
			t.Errorf("%s mode=%v", name, info.Mode())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.key")); !os.IsNotExist(err) {
		t.Fatal("CA key should not be persisted")
	}
	cert, roots, err := tunnel.LoadIdentity(filepath.Join(dir, "connector.crt"), filepath.Join(dir, "connector.key"), filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tunnel.ClientTLS(cert, roots, "edge.localhost"); err != nil {
		t.Fatal(err)
	}
}

func TestTunnelSpikeDefaultEdgeStopsOnCancellation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pki")
	if err := tunnel.InitLab(dir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runTunnelSpike(ctx, []string{"edge", "-pki", dir}, io.Discard)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("edge CLI leaked workers")
	}
}

func TestConnectorRoutesAreFixedAndBounded(t *testing.T) {
	// Absolute on every OS: "/srv/x" has no drive letter, so Windows rejects it.
	portfolio, docs := filepath.Join(t.TempDir(), "portfolio"), filepath.Join(t.TempDir(), "docs")
	routes, err := connectorRoutes("portfolio.localhost", portfolio, "", []string{
		"api.localhost=upstream:http://127.0.0.1:5177",
		"docs.localhost=root:" + docs,
	})
	if err != nil {
		t.Fatalf("connectorRoutes: %v", err)
	}
	if len(routes) != 3 || routes[0].Host != "portfolio.localhost" || routes[0].Root != portfolio ||
		routes[1].Target != "http://127.0.0.1:5177" || routes[2].Root != docs {
		t.Fatalf("routes = %+v", routes)
	}

	// The extra routes are optional: without them it is exactly the old single route.
	only, err := connectorRoutes("portfolio.localhost", "", "http://127.0.0.1:8000", nil)
	if err != nil || len(only) != 1 || only[0].Target != "http://127.0.0.1:8000" {
		t.Fatalf("single route = %+v, %v", only, err)
	}

	for name, extra := range map[string][]string{
		"missing separator":      {"api.localhost"},
		"unknown kind":           {"api.localhost=proxy:http://127.0.0.1:1"},
		"no kind prefix":         {"api.localhost=/srv/docs"},
		"relative root":          {"api.localhost=root:docs"},
		"public upstream":        {"api.localhost=upstream:http://192.0.2.1:80"},
		"https upstream":         {"api.localhost=upstream:https://127.0.0.1:443"},
		"upstream with path":     {"api.localhost=upstream:http://127.0.0.1:80/api"},
		"upstream credentials":   {"api.localhost=upstream:http://user:pw@127.0.0.1:80"},
		"duplicates the primary": {"portfolio.localhost=root:/srv/other"},
		"duplicate extra":        {"a.localhost=root:/srv/a", "A.localhost=root:/srv/b"},
		"empty host":             {"=root:/srv/a"},
		"bad host":               {"not a host=root:/srv/a"},
	} {
		if _, err := connectorRoutes("portfolio.localhost", "/srv/portfolio", "", extra); err == nil {
			t.Errorf("%s: connectorRoutes accepted %v", name, extra)
		}
	}

	var many []string
	for i := 0; i < maxConnectorRoutes; i++ { // primary + this many exceeds the cap
		many = append(many, fmt.Sprintf("h%d.localhost=root:/srv/h%d", i, i))
	}
	if _, err := connectorRoutes("portfolio.localhost", "/srv/portfolio", "", many); err == nil {
		t.Error("the number of routes a connector serves must be bounded")
	}
}
