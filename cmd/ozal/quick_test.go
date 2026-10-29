package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func TestInitCreatesPrivateValidConfigWithoutOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	var out bytes.Buffer
	if err := runInit(context.Background(), []string{"-config", path}, &out); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if !strings.Contains(out.String(), "mode 0600") {
		t.Fatalf("init output does not describe permissions: %q", out.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); hasUnixPermissions() && got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("generated config is invalid: %v", err)
	}

	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runInit(context.Background(), []string{"-config", path}, &bytes.Buffer{}); err == nil {
		t.Fatal("init overwrote an existing file")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing file = %q, error = %v", data, err)
	}
}

func TestDoctorSuppressesConfigurationValuesOnFailure(t *testing.T) {
	secret := "doctor-must-never-print-this-value"
	path := filepath.Join(t.TempDir(), "bad-config.json")
	data := []byte(`{"listen":"","admin":{"token":"` + secret + `"},"routes":[]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runDoctor(context.Background(), []string{"-config", path}, &out)
	if err == nil {
		t.Fatal("doctor accepted invalid config")
	}
	if strings.Contains(out.String(), secret) || strings.Contains(err.Error(), secret) {
		t.Fatalf("doctor leaked a config value: output=%q error=%q", out.String(), err)
	}
	if !strings.Contains(out.String(), "details are suppressed") {
		t.Fatalf("doctor did not explain its redaction: %q", out.String())
	}
}

func TestDoctorReportsStateRequiredWithoutCreatingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// pages.dir must be absolute on the running OS; a POSIX literal is not on Windows.
	pagesDir, err := json.Marshal(filepath.Join(dir, "pages"))
	if err != nil {
		t.Fatal(err)
	}
	configJSON := `{"listen":"127.0.0.1:8888","pages":{"dir":` + string(pagesDir) + `},"routes":[{"host":"site.localhost:8888","site":"docs"}]}`
	if err := os.WriteFile(path, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = runDoctor(context.Background(), []string{"-config", path}, &out)
	if err == nil || !strings.Contains(out.String(), "needs persistent state") {
		t.Fatalf("doctor result = %q, error = %v", out.String(), err)
	}
}

func TestDoctorChecksMigratedDatabaseReadOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	dbPath := filepath.Join(dir, "state.db")
	configJSON := `{"listen":"127.0.0.1:8888","routes":[{"host":"app.localhost:8888","target":"http://127.0.0.1:3000"}]}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	prepareStateDatabase(t, ctx, dbPath)
	var out bytes.Buffer
	if err := runDoctor(ctx, []string{"-config", configPath, "-db", dbPath}, &out); err != nil {
		t.Fatalf("doctor: %v (%s)", err, out.String())
	}
	if !strings.Contains(out.String(), "integrity and migration history match") {
		t.Fatalf("doctor output = %q", out.String())
	}
}

func TestSchemaAndCompletionCommands(t *testing.T) {
	var schemaOut bytes.Buffer
	if err := runSchema(context.Background(), nil, &schemaOut); err != nil {
		t.Fatalf("runSchema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaOut.Bytes(), &schema); err != nil {
		t.Fatalf("schema output is invalid JSON: %v", err)
	}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		var out bytes.Buffer
		if err := runCompletion(context.Background(), []string{shell}, &out); err != nil {
			t.Errorf("completion %s: %v", shell, err)
			continue
		}
		if !strings.Contains(out.String(), "ozal") || !strings.Contains(out.String(), "doctor") ||
			!strings.Contains(out.String(), "audit") || !strings.Contains(out.String(), "keygen") || !strings.Contains(out.String(), "repair") ||
			!strings.Contains(out.String(), "checkpoint") || !strings.Contains(out.String(), "verify") ||
			!strings.Contains(out.String(), "who-can") || !strings.Contains(out.String(), "review") ||
			!strings.Contains(out.String(), "runtime") || !strings.Contains(out.String(), "prepare") ||
			!strings.Contains(out.String(), "sign") || !strings.Contains(out.String(), "check") ||
			!strings.Contains(out.String(), "login") || !strings.Contains(out.String(), "disconnect") {
			t.Errorf("completion %s is incomplete: %q", shell, out.String())
		}
	}
	if err := runCompletion(context.Background(), []string{"unknown"}, &bytes.Buffer{}); err == nil {
		t.Fatal("completion accepted an unsupported shell")
	}
}

func TestVersionTextAndJSON(t *testing.T) {
	var textOut bytes.Buffer
	if err := runVersion(context.Background(), nil, &textOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOut.String(), "ozal") || !strings.Contains(textOut.String(), "platform:") {
		t.Fatalf("text version output = %q", textOut.String())
	}
	var jsonOut bytes.Buffer
	if err := runVersion(context.Background(), []string{"-json"}, &jsonOut); err != nil {
		t.Fatal(err)
	}
	var info versionInfo
	if err := json.Unmarshal(jsonOut.Bytes(), &info); err != nil {
		t.Fatalf("JSON version output: %v", err)
	}
	if info.Name != "ozal" || info.Version == "" || info.Go == "" {
		t.Fatalf("version info = %+v", info)
	}
}
