package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/tunnel"
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
		if info.Mode().Perm() != 0600 {
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
