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
			got := edgeConnectorArgs(base, tc.sub, tc.extra...)
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
