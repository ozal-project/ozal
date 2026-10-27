package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestFirstRunNoticeCarriesTheLinkAndThePort(t *testing.T) {
	got := firstRunNotice("127.0.0.1:9091", "TOKEN")
	for _, want := range []string{"http://localhost:9091/overview#enrol=TOKEN", "ssh -N -L 9091:127.0.0.1:9091"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestSetupLinkFileIsPrivateAndReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup-link")
	if err := writeSetupLink(path, "old"); err != nil {
		t.Fatal(err)
	}
	if err := writeSetupLink(path, "new"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("setup link mode = %v %v", info.Mode(), err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new\n" {
		t.Fatalf("not replaced: %q", got)
	}
}
