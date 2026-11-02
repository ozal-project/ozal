package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalledUpdateRunnerStagesBesideInstalledBinary(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "ozal")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' \"$TMPDIR\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "noexec"))

	var out bytes.Buffer
	if err := (installedUpdateRunner{binary: binary}).Run(context.Background(), nil, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != dir {
		t.Fatalf("TMPDIR = %q, want installed binary directory %q", got, dir)
	}
}

func TestUpdateExecutorUnitHasOnlyTheCapabilityNeededForMigration(t *testing.T) {
	unit, err := os.ReadFile("../../docs/operations/ozal-update-executor.service")
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	for _, want := range []string{"NoNewPrivileges=true", "AmbientCapabilities=CAP_SETUID"} {
		if !strings.Contains(text, want) {
			t.Errorf("update executor unit is missing %q", want)
		}
	}
	ambient := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "AmbientCapabilities=") {
			if ambient != "" {
				t.Fatal("update executor unit has multiple ambient capability declarations")
			}
			ambient = line
		}
	}
	if ambient != "AmbientCapabilities=CAP_SETUID" {
		t.Fatalf("ambient capabilities = %q, want only CAP_SETUID", ambient)
	}
}
