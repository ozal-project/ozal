package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func eabConfig(t *testing.T, fileContent string) *config.DNS01 {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eab.key")
	if err := os.WriteFile(path, []byte(fileContent), 0o600); err != nil {
		t.Fatal(err)
	}
	return &config.DNS01{ExternalAccount: &config.DNS01ExternalAccount{KeyID: "kid-1", HMACKey: "file:" + path}}
}

func TestExternalAccountIsOptional(t *testing.T) {
	ea, err := externalAccount(&config.DNS01{})
	if err != nil || ea != nil {
		t.Fatalf("externalAccount = %v, %v; want none", ea, err)
	}
}

func TestExternalAccountDecodesTheCAKey(t *testing.T) {
	raw := []byte{0xfb, 0xff, 0xfe, 0x01, 0x02, 0x03, 0x04} // base64url differs from standard here
	for name, encoded := range map[string]string{
		"unpadded base64url": base64.RawURLEncoding.EncodeToString(raw) + "\n",
		"padded base64url":   base64.URLEncoding.EncodeToString(raw),
		"standard base64":    base64.StdEncoding.EncodeToString(raw),
	} {
		t.Run(name, func(t *testing.T) {
			ea, err := externalAccount(eabConfig(t, encoded))
			if err != nil {
				t.Fatal(err)
			}
			if ea.KeyID != "kid-1" || !bytes.Equal(ea.HMACKey, raw) {
				t.Fatalf("decoded key mismatch: %q %x", ea.KeyID, ea.HMACKey)
			}
		})
	}
}

func TestExternalAccountRefusesAKeyItCannotDecode(t *testing.T) {
	_, err := externalAccount(eabConfig(t, "not*base64!!"))
	if err == nil || !strings.Contains(err.Error(), "external_account.hmac_key") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "not*base64") {
		t.Fatal("the error echoes the secret value")
	}
}
