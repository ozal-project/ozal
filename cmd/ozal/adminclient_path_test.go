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
	t.Setenv("OZAL_CONFIG", "/etc/ozal-preview/routing-preview.json")
	if got := resolveConfigPath("config.json"); got != "/etc/ozal-preview/routing-preview.json" {
		t.Fatalf("env ignored: %q", got)
	}
	if got := resolveConfigPath("/srv/other.json"); got != "/srv/other.json" {
		t.Fatalf("explicit flag lost to env: %q", got)
	}
}

func TestLegacyConfigEnvironmentStillWorksAndOzalTakesPrecedence(t *testing.T) {
	t.Setenv("OZAL_CONFIG", "")
	t.Setenv("SWITCHBOARD_CONFIG", "/srv/legacy/config.json")
	if got := resolveConfigPath("config.json"); got != "/srv/legacy/config.json" {
		t.Fatalf("legacy config environment ignored: %q", got)
	}
	t.Setenv("OZAL_CONFIG", "/srv/ozal/config.json")
	if got := resolveConfigPath("config.json"); got != "/srv/ozal/config.json" {
		t.Fatalf("Ozal config did not take precedence: %q", got)
	}
}

func TestAdminTokenEnvironmentCompatibility(t *testing.T) {
	t.Setenv("SWITCHBOARD_ADMIN_TOKEN", "legacy-fixture")
	t.Setenv("OZAL_ADMIN_TOKEN", "")
	if err := os.Unsetenv("OZAL_ADMIN_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if got := defaultAdminTokenRef(); got != "env:SWITCHBOARD_ADMIN_TOKEN" {
		t.Fatalf("legacy token reference = %q", got)
	}
	t.Setenv("OZAL_ADMIN_TOKEN", "new-fixture")
	if got := defaultAdminTokenRef(); got != "env:OZAL_ADMIN_TOKEN" {
		t.Fatalf("new token did not win: %q", got)
	}
	// Explicitly empty new configuration must fail closed, not use old authority.
	t.Setenv("OZAL_ADMIN_TOKEN", "")
	if got := defaultAdminTokenRef(); got != "env:OZAL_ADMIN_TOKEN" {
		t.Fatalf("empty new token fell back to old authority: %q", got)
	}
}

func TestMigrationDefaultKeepsLegacyState(t *testing.T) {
	t.Chdir(t.TempDir())
	if got := migrationDBPath(); got != "ozal.db" {
		t.Fatal(got)
	}
	if err := os.WriteFile("switchboard.db", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := migrationDBPath(); got != "switchboard.db" {
		t.Fatalf("legacy state ignored: %q", got)
	}
	if err := os.WriteFile("ozal.db", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := migrationDBPath(); got != "ozal.db" {
		t.Fatalf("new state ignored: %q", got)
	}
}

func TestForwardedURLFollowsTheAdminPort(t *testing.T) {
	if got := forwardedURL("127.0.0.1:9091"); got != "http://ozal.localhost:9091" {
		t.Fatal(got)
	}
	if got := forwardedURL(""); got != "http://ozal.localhost:9090" {
		t.Fatal(got)
	}
}

func TestNewPasskeyLinksUseOzalFromLegacyConsoleAddresses(t *testing.T) {
	for _, base := range []string{"http://localhost:9091", "http://switchboard.localhost:9091", "http://127.0.0.1:9091", "http://ozal.localhost:9091"} {
		if got := passkeyOrigin(base); got != "http://ozal.localhost:9091" {
			t.Fatalf("%s became %s", base, got)
		}
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
	onPath := func(string) string { return "/opt/sb/ozal" }
	if got := enrolCommand("/opt/sb/ozal", installedConfig, onPath); got != "sudo ozal console enrol" {
		t.Fatal(got)
	}
	if got := enrolCommand("/opt/sb/ozal", "/etc/x/routing.json", onPath); got != "sudo ozal console enrol -config /etc/x/routing.json" {
		t.Fatal(got)
	}
	if got := enrolCommand("/opt/sb/ozal", installedConfig, none); got != "sudo /opt/sb/ozal console enrol" {
		t.Fatal(got)
	}
	// Another ozal earlier on the PATH must not be advertised.
	if got := enrolCommand("/opt/sb/ozal", installedConfig, func(string) string { return "/usr/bin/ozal" }); got != "sudo /opt/sb/ozal console enrol" {
		t.Fatal(got)
	}
}

func TestFirstRunNoticeCarriesTheLinkAndThePort(t *testing.T) {
	got := firstRunNotice("127.0.0.1:9091", "TOKEN")
	for _, want := range []string{"http://ozal.localhost:9091/overview#enrol=TOKEN", "ssh -N -L 9091:127.0.0.1:9091"} {
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
