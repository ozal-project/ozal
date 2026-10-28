package certs

import (
	"testing"
	"time"
)

// TestRenewalDue covers docs/engineering.md#certificados:
// the boundary, unusable intervals and the clock-in-another-zone case.
func TestRenewalDue(t *testing.T) {
	start := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(90 * 24 * time.Hour)
	boundary := start.Add(60 * 24 * time.Hour)
	tests := []struct {
		name                     string
		now, notBefore, notAfter time.Time
		want                     bool
	}{
		{"just issued", start, start, end, false},
		{"before boundary", boundary.Add(-time.Nanosecond), start, end, false},
		{"at boundary", boundary, start, end, true},
		{"after boundary", boundary.Add(time.Nanosecond), start, end, true},
		{"at expiry", end, start, end, true},
		{"expired", end.Add(time.Hour), start, end, true},
		{"not yet valid", start.Add(-time.Second), start, end, true},
		{"zero notBefore", start, time.Time{}, end, true},
		{"zero notAfter", start, start, time.Time{}, true},
		{"empty interval", start, start, start, true},
		{"reversed interval", start, end, start, true},
		{"short lifetime before boundary", start.Add(3*time.Hour - time.Nanosecond), start, start.Add(4*time.Hour + 30*time.Minute), false},
		{"short lifetime at boundary", start.Add(3 * time.Hour), start, start.Add(4*time.Hour + 30*time.Minute), true},
		{"same instant in another zone", boundary.In(time.FixedZone("UTC-3", -3*60*60)), start, end, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenewalDue(tt.now, tt.notBefore, tt.notAfter); got != tt.want {
				t.Fatalf("RenewalDue() = %v, want %v", got, tt.want)
			}
		})
	}
}
