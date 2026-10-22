package tunnelrenew

import (
	"path/filepath"
	"slices"
	"testing"
)

// The renewal changes the registry through the running edge, never behind
// it: no -db, the config that names the admin socket instead.
func TestEdgeConnectorChangesGoThroughTheAdminSocket(t *testing.T) {
	base := filepath.Join(t.TempDir(), "edge with spaces")
	cert := filepath.Join(base, "replacement certificate.crt")
	for _, tc := range []struct {
		sub   string
		extra []string
	}{
		{sub: "current"},
		{sub: "rotate", extra: []string{"-expected", "ab", "-cert", cert}},
	} {
		t.Run(tc.sub, func(t *testing.T) {
			got := edgeConnectorArgs(filepath.Join(base, "config.integrated.json"), tc.sub, tc.extra...)
			// This command runs on the edge host, so its config path uses
			// native separators. Compare argv, not a shell command: spaces
			// must remain inside one argument, and -db must never appear.
			want := []string{"connector", tc.sub, "-config", filepath.Join(base, "config.integrated.json"), "-name", "portfolio"}
			want = append(want, tc.extra...)
			if !slices.Equal(got, want) {
				t.Fatalf("args = %q\nwant  %q", got, want)
			}
		})
	}
}

func TestAgentLayouts(t *testing.T) {
	user := Agent{Home: filepath.Join("home", "u"), Role: "connector"}
	if got := user.restartCommand(); !slices.Equal(got, []string{"systemctl", "--user", "restart", "switchboard-tunnel-connector.service"}) {
		t.Fatalf("user unit restart = %q", got)
	}
	if user.config() != filepath.Join("home", "u", ".local/share/switchboard-tunnel-connector", "config.integrated.json") {
		t.Fatalf("user config = %q", user.config())
	}
	system := Agent{Role: "edge", Dir: "/var/lib/e", Config: "/etc/e/config.json", Binary: "/usr/local/lib/e/switchboard", Unit: "switchboard-tunnel-edge"}
	// The sudoers rule matches this exact argv.
	if got := system.restartCommand(); !slices.Equal(got, []string{"sudo", "-n", "/usr/bin/systemctl", "restart", "switchboard-tunnel-edge"}) {
		t.Fatalf("system unit restart = %q", got)
	}
	if system.base() != "/var/lib/e" || system.config() != "/etc/e/config.json" || system.binary() != "/usr/local/lib/e/switchboard" {
		t.Fatalf("system layout = %q %q %q", system.base(), system.config(), system.binary())
	}
}
