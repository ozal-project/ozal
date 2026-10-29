package config

import (
	"strings"
	"testing"
)

func externalAccountTLS(block string) string {
	return `{
		"enabled": true,
		"domains": ["preview.example.com"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/ozal/certs",
			"directory_url": "https://dv.acme-v02.api.pki.goog/directory",
			"dns_token": "env:TOKEN"` + block + `
		}
	}`
}

func TestDNS01ExternalAccountIsAccepted(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, externalAccountTLS(`,
			"external_account": {"key_id": " kid-123 ", "hmac_key": "file:/etc/ozal/eab.key"}`))
	ea := cfg.TLS.DNS01.ExternalAccount
	if ea == nil || ea.KeyID != "kid-123" || ea.HMACKey != "file:/etc/ozal/eab.key" {
		t.Fatalf("external_account = %+v", ea)
	}
	clone := Clone(*cfg)
	if clone.TLS.DNS01.ExternalAccount == ea {
		t.Fatal("Clone shares the external_account pointer")
	}
	if clone.TLS.DNS01.ExternalAccount.KeyID != "kid-123" {
		t.Fatal("Clone dropped external_account")
	}
}

func TestDNS01ExternalAccountRefusals(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	tests := []struct{ name, block, want string }{
		{"inline key", `, "external_account": {"key_id": "k", "hmac_key": "c2VjcmV0"}`, "env: or file: reference"},
		{"no key", `, "external_account": {"key_id": "k"}`, "env: or file: reference"},
		{"no id", `, "external_account": {"hmac_key": "env:EAB"}`, "key_id"},
		{"blank id", `, "external_account": {"key_id": "  ", "hmac_key": "env:EAB"}`, "key_id"},
		{"empty reference", `, "external_account": {"key_id": "k", "hmac_key": "file: "}`, "names no file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, `{
				"listen": ":8888",
				"tls": `+externalAccountTLS(tt.block)+`,
				"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
			}`)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load error = %v, want it to mention %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "c2VjcmV0") {
				t.Fatal("the error echoes the secret value")
			}
		})
	}
}
