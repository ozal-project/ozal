package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigPathKeepsAnExplicitPathAndPrefersALocalFile(t *testing.T) {
	if got := resolveConfigPath("/srv/other.json"); got != "/srv/other.json" {
		t.Fatalf("explicit path changed to %q", got)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveConfigPath("config.json"); got != "config.json" {
		t.Fatalf("a config beside the shell was ignored: %q", got)
	}
}

func TestEnvironmentConfigBeatsLocalAndInstalledButNotTheFlag(t *testing.T) {
	t.Setenv("SWITCHBOARD_CONFIG", "/etc/switchboard-preview/routing-preview.json")
	if got := resolveConfigPath("config.json"); got != "/etc/switchboard-preview/routing-preview.json" {
		t.Fatalf("env ignored: %q", got)
	}
	if got := resolveConfigPath("/srv/other.json"); got != "/srv/other.json" {
		t.Fatalf("explicit flag lost to env: %q", got)
	}
}

func TestForwardedURLFollowsTheAdminPort(t *testing.T) {
	if got := forwardedURL("127.0.0.1:9091"); got != "http://127.0.0.1:9091" {
		t.Fatal(got)
	}
	if got := forwardedURL(""); got != "http://127.0.0.1:9090" {
		t.Fatal(got)
	}
}

func TestSSHHintOnlyInsideSSH(t *testing.T) {
	got := sshHint("10.8.0.4 50000 10.8.0.1 22", "octavio", "http://127.0.0.1:9091")
	want := "From your computer, forward the port first, then open the link:\n  ssh -N -L 9091:127.0.0.1:9091 octavio@10.8.0.1\n"
	if got != want {
		t.Fatalf("hint = %q", got)
	}
	if sshHint("", "octavio", "http://127.0.0.1:9091") != "" {
		t.Fatal("hint printed outside an SSH session")
	}
}

func TestEnrolCommandIsShortOnlyWhenItIsTrue(t *testing.T) {
	none := func(string) string { return "" }
	onPath := func(string) string { return "/opt/sb/switchboard" }
	if got := enrolCommand("/opt/sb/switchboard", installedConfig, onPath); got != "sudo switchboard console enrol" {
		t.Fatal(got)
	}
	if got := enrolCommand("/opt/sb/switchboard", "/etc/x/routing.json", onPath); got != "sudo switchboard console enrol -config /etc/x/routing.json" {
		t.Fatal(got)
	}
	if got := enrolCommand("/opt/sb/switchboard", installedConfig, none); got != "sudo /opt/sb/switchboard console enrol" {
		t.Fatal(got)
	}
	// Another switchboard earlier on the PATH must not be advertised.
	if got := enrolCommand("/opt/sb/switchboard", installedConfig, func(string) string { return "/usr/bin/switchboard" }); got != "sudo /opt/sb/switchboard console enrol" {
		t.Fatal(got)
	}
}
