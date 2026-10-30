package certs

import (
	"fmt"
	"strings"
	"time"
)

// A zone is a registered domain the edge serves, such as "example.com"
// (EDGE-008). Each zone has its own certificate for the apex and one level
// of subdomains — "example.com" and "*.example.com", the shape of a
// Universal SSL certificate — its own storage and its own renewal, so a
// zone whose challenge fails never holds back another. This file is the pure
// part: which names a zone carries, which zone serves a hostname and how its
// state reads to an operator.

// zoneKeyPrefix is the storage key of a zone certificate. It is distinct
// from every single-certificate key (a hostname or "_wildcard.<base>"), so
// the certificate in service before zones keeps its files while each zone
// issues its own.
const zoneKeyPrefix = "_zone."

// NormalizeZone lowercases a zone and drops a trailing root dot.
func NormalizeZone(zone string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone), "."))
}

// ValidateZone accepts a public domain with at least two labels: a zone is
// never a wildcard, an IP literal, a single label or under .localhost.
func ValidateZone(zone string) error {
	if zone != NormalizeZone(zone) {
		return fmt.Errorf("certs: zone %q must be lowercase without a trailing dot", zone)
	}
	if IsWildcard(zone) || !PublicName(zone) {
		return fmt.Errorf("certs: %q is not a public domain (example.com)", zone)
	}
	if err := ValidateName(wildcardPrefix + zone); err != nil {
		return err
	}
	return nil
}

// ZoneNames lists the names on a zone's certificate, its identity first.
func ZoneNames(zone string) []string { return []string{wildcardPrefix + zone, zone} }

// ZoneStorageKey is where a zone's certificate generations are stored.
func ZoneStorageKey(zone string) string { return zoneKeyPrefix + zone }

// ServingZone returns the zone whose certificate covers serverName. Nested
// zones are allowed ("example.com" and "lab.example.com"); "lab.example.com"
// is then the apex of the second and a subdomain of the first, and the more
// specific zone — the one the operator added for it — wins.
func ServingZone(zones []string, serverName string) (string, bool) {
	name := strings.ToLower(strings.TrimSuffix(serverName, "."))
	best := ""
	for _, zone := range zones {
		if CoversAny(ZoneNames(zone), name) && len(zone) > len(best) {
			best = zone
		}
	}
	return best, best != ""
}

// ZoneState is the operator-facing state of one zone's certificate.
type ZoneState string

const (
	// ZonePending: no certificate yet and no attempt made.
	ZonePending ZoneState = "pending"
	// ZoneQueued: waiting for another zone's issuance to finish.
	ZoneQueued ZoneState = "queued"
	// ZoneIssuing: the first certificate is being issued.
	ZoneIssuing ZoneState = "issuing"
	// ZoneActive: a valid certificate is served.
	ZoneActive ZoneState = "active"
	// ZoneRenewing: a valid certificate is served while its successor is issued.
	ZoneRenewing ZoneState = "renewing"
	// ZoneRenewalFailed: still served, but the last renewal failed.
	ZoneRenewalFailed ZoneState = "renewal_failed"
	// ZoneExpiring: still served, less than ExpiringWithin of validity left.
	ZoneExpiring ZoneState = "expiring"
	// ZoneFailed: no valid certificate and the last attempt failed.
	ZoneFailed ZoneState = "failed"
)

// ExpiringWithin is when a served certificate is reported as expiring. The
// manager renews at two thirds of the lifetime (30 days for 90-day
// certificates), so reaching this means renewal has been failing for weeks.
const ExpiringWithin = 7 * 24 * time.Hour

// StateOf reads a manager status as a zone state at now.
func StateOf(s Status, now time.Time) ZoneState {
	if !s.Valid {
		switch {
		case s.Attempting:
			return ZoneIssuing
		case s.Waiting:
			return ZoneQueued
		case s.LastOutcome == "failed":
			return ZoneFailed
		default:
			return ZonePending
		}
	}
	switch {
	case s.NotAfter.Sub(now) < ExpiringWithin:
		return ZoneExpiring
	case s.Attempting:
		return ZoneRenewing
	case s.LastOutcome == "failed":
		return ZoneRenewalFailed
	default:
		return ZoneActive
	}
}
