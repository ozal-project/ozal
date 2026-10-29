package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeValidateConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateAcceptsACandidate(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	path := writeValidateConfig(t, `{
	  "listen": "127.0.0.1:18888",
	  "tls": {
	    "enabled": true,
	    "domains": ["preview.example.com"],
	    "email": "owner@example.com",
	    "origin_client_ca_file": "/run/ozal/origin-ca.pem",
	    "dns01": {
	      "state_dir": "/var/lib/ozal/origin-cert",
	      "directory_url": "https://acme-v02.api.letsencrypt.org/directory",
	      "dns_token": "file:/run/ozal/cloudflare-token"
	    }
	  },
	  "trusted_proxies": ["173.245.48.0/20", "2400:cb00::/32"],
	  "tunnel": {
	    "listen": "127.0.0.1:9443",
	    "network": "10.8.0.0/24",
	    "cert_file": "/run/ozal/tunnel.pem",
	    "key_file": "/run/ozal/tunnel.key",
	    "ca_file": "/run/ozal/tunnel-ca.pem"
	  },
	  "routes": [
	    { "host": "preview.example.com", "target": "http://127.0.0.1:18889" },
	    { "host": "app.example.com", "connector": "homelab" }
	  ]
	}`)

	var out bytes.Buffer
	if err := runValidate(context.Background(), []string{"-config", path}, &out); err != nil {
		t.Fatalf("runValidate: %v", err)
	}
	line := out.String()
	for _, want := range []string{"routes=2", "protected=0", "tunnel=1", "tls=true", "dns01=true", "trusted_proxies=2", "admin=127.0.0.1:9090"} {
		if !strings.Contains(line, want) {
			t.Errorf("validate output missing %q: %s", want, line)
		}
	}
}

func TestValidateRejectsInvalidInput(t *testing.T) {
	bad := writeValidateConfig(t, `{"listen": "", "routes": []}`)
	if err := runValidate(context.Background(), []string{"-config", bad}, &bytes.Buffer{}); err == nil {
		t.Fatal("runValidate accepted an invalid config")
	}
	if err := runValidate(context.Background(), []string{"-config", filepath.Join(t.TempDir(), "missing.json")}, &bytes.Buffer{}); err == nil {
		t.Fatal("runValidate accepted a missing config")
	}
}
