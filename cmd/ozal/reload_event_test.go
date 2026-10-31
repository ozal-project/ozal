package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/events"
)

// A failed SIGHUP reload must never reach observers as a successful one: the
// sinks clear Event.Error, so the type and code have to carry the failure.
func TestSIGHUPReloadEventReportsFailureWithoutRawError(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	raw := errors.New("open /etc/ozal/secret-dir/config.json: permission denied")

	failed := sighupReloadEvent(raw, "abc123", now)
	if failed.Type != events.ConfigReloadFailed || failed.Code != "config.reload_failed" {
		t.Fatalf("failed reload = %s/%s, want config.reload_failed", failed.Type, failed.Code)
	}
	if failed.Error != "" || strings.Contains(failed.Reason, "secret-dir") {
		t.Fatalf("failed reload carries raw error text: %+v", failed)
	}
	if failed.Actor != "signal:SIGHUP" || failed.ConfigSHA256 != "abc123" || !failed.Time.Equal(now) {
		t.Fatalf("failed reload lost its context: %+v", failed)
	}

	ok := sighupReloadEvent(nil, "def456", now)
	if ok.Type != events.ConfigReloaded || ok.Code != "" || ok.ConfigSHA256 != "def456" {
		t.Fatalf("successful reload = %+v", ok)
	}
}
