package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSecret(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretFile, []byte("  from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyFile := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(emptyFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SB_TEST_SECRET", "from-env")

	cases := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{"inline stays inline", "literal-secret", "literal-secret", false},
		{"environment", "env:SB_TEST_SECRET", "from-env", false},
		{"file is trimmed", "file:" + secretFile, "from-file", false},
		{"unset environment", "env:SB_UNSET_SECRET", "", true},
		{"missing file", "file:" + filepath.Join(t.TempDir(), "nope"), "", true},
		{"empty file", "file:" + emptyFile, "", true},
		{"empty env name", "env:", "", true},
		{"empty file name", "file:", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSecret(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveSecret(%q) = %q, want error", tc.value, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ResolveSecret(%q) = (%q, %v), want (%q, nil)", tc.value, got, err, tc.want)
			}
		})
	}
}

func TestValidateSecretReference(t *testing.T) {
	for _, value := range []string{"inline", "env:NAME", "file:/run/secrets/token"} {
		if err := ValidateSecretReference("admin.token", value); err != nil {
			t.Errorf("ValidateSecretReference(%q): %v", value, err)
		}
	}
	for _, value := range []string{"env:", "file:", "env:   "} {
		if err := ValidateSecretReference("admin.token", value); err == nil {
			t.Errorf("ValidateSecretReference(%q) accepted an empty reference", value)
		}
	}
}

func TestValidateRejectsEmptySecretReference(t *testing.T) {
	cfg := Config{
		Listen: ":8888",
		Routes: []Route{{
			Host:   "app.localhost",
			Target: "http://localhost:9000",
			Access: &AccessPolicy{},
		}},
		Auth: Auth{
			Issuer:       "https://issuer.example.com",
			ClientID:     "client",
			ClientSecret: "env:",
			RedirectURL:  "http://app.localhost/_/auth/callback",
		},
	}
	if _, err := Validate(cfg); err == nil {
		t.Fatal("Validate accepted an empty env secret reference")
	}

	cfg.Auth.ClientSecret = "env:SB_CLIENT_SECRET"
	if _, err := Validate(cfg); err != nil {
		t.Fatalf("Validate(valid reference): %v", err)
	}
}
