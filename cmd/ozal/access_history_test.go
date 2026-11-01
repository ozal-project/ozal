package main

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/accesshistory"
	"github.com/ozal-project/ozal/internal/audit"
	"github.com/ozal-project/ozal/internal/events"
)

// The console's access history survives a restart when access is audited:
// a new process rebuilds it from the journal, and the live feed it replaced
// counts nothing more (UI-005).
func TestAccessHistoryComesBackFromTheJournalAfterARestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("owner-only audit storage is not supported on Windows yet")
	}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	run := func(appends ...events.Event) *accesshistory.Store {
		t.Helper()
		journal := accesshistory.NewStore(accesshistory.Journal, time.Time{}, 0, 0)
		writer, err := audit.Open(ctx, dir, audit.Options{Observe: observeAccess(journal)})
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		for _, e := range appends {
			if err := writer.Append(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		return journal
	}
	run(
		events.Event{Type: events.AccessAllowed, Actor: "sb:ana", Host: "grafana.example.com", Code: "allowed"},
		events.Event{Type: events.AccessDenied, Actor: "sb:bob", Host: "grafana.example.com", Code: "email_not_allowed", RemoteAddr: "203.0.113.7"},
	)

	restarted := run()
	sum := restarted.Summary(10)
	if sum.Source != accesshistory.Journal || sum.Allowed != 1 || sum.Refused != 1 || sum.Since.IsZero() {
		t.Fatalf("after restart = %+v, want both decisions from the journal", sum)
	}
	page := restarted.Query(accesshistory.Filter{Outcome: accesshistory.Refused})
	if len(page.Entries) != 1 || page.Entries[0].Remote != "203.0.113.7" || page.Entries[0].Code != "email_not_allowed" {
		t.Fatalf("refusals = %+v", page.Entries)
	}

	memory := accesshistory.NewStore(accesshistory.Memory, time.Now(), 0, 0)
	live := &liveAccess{store: memory}
	live.Emit(events.Event{Type: events.AccessAllowed, Actor: "sb:ana", Host: "grafana.example.com"})
	live.off.Store(true)
	live.Emit(events.Event{Type: events.AccessAllowed, Actor: "sb:ana", Host: "grafana.example.com"})
	if got := memory.Summary(1).Allowed; got != 1 {
		t.Fatalf("live feed counted %d after the journal took over, want 1", got)
	}
}
