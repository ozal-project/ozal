package main

import (
	"slices"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

// publish runs under the reload lock: it never blocks, and the certificate
// set only ever needs the newest zones.
func TestZoneUpdatesKeepOnlyTheNewestSet(t *testing.T) {
	updates := make(zoneUpdates, 1)
	for _, zone := range []string{"a.dev", "b.dev", "c.dev"} {
		cfg := config.Config{TLS: config.TLS{Enabled: true, DNS01: &config.DNS01{}}, Zones: []config.Zone{{Name: zone}}}
		updates.publish(cfg) // would deadlock if it blocked on a full channel
	}
	if got := <-updates; !slices.Equal(got, []string{"c.dev"}) {
		t.Fatalf("pending set = %v, want only the newest", got)
	}
	select {
	case extra := <-updates:
		t.Fatalf("stale set left behind: %v", extra)
	default:
	}
}
