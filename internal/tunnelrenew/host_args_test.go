package tunnelrenew

import (
	"strings"
	"testing"
)

// The renewal changes the registry through the running edge, never behind
// it: no -db, the config that names the admin socket instead.
func TestEdgeConnectorChangesGoThroughTheAdminSocket(t *testing.T) {
	got := strings.Join(edgeConnectorArgs("/edge", "rotate", "-expected", "ab", "-cert", "/tmp/c.crt"), " ")
	want := "connector rotate -config /edge/config.integrated.json -name portfolio -expected ab -cert /tmp/c.crt"
	if got != want {
		t.Fatalf("args = %q\nwant  %q", got, want)
	}
	if strings.Contains(strings.Join(edgeConnectorArgs("/edge", "current"), " "), "-db") {
		t.Fatal("current must not open the state database directly")
	}
}
