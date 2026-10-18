// Package certs owns Switchboard's public origin certificate: the pure
// decisions about it, the manager that serves and renews it, and the
// adapters that talk to ACME and to the DNS provider (EDGE-007).
//
// The split follows the house pattern. renewal.go and validate.go are pure
// and import only the standard library: the renewal boundary and the checks
// a stored certificate must pass before it is trusted. manager.go is the
// shell: one active issuance attempt, exponential backoff with jitter,
// atomic activation and fail-closed serving. filestore.go, acmeissuer.go and
// cloudflare.go are adapters for one technology each. cmd/switchboard is the
// only place that constructs them.
//
// The scope is deliberately bounded: one hostname, Cloudflare DNS, no
// wildcards. Wildcards stay in EDGE-002; replacing the manager later must
// not change the tls.Config.GetCertificate contract the listener uses.
package certs

import (
	"errors"

	"github.com/octaviosantacruz/switchboard/internal/events"
)

// Event types published by the manager. Like every event type in the
// project these names are stable: renaming one breaks whoever reads the
// JSON logs.
const (
	CertRenewed       events.Type = "cert.renewed"
	CertRenewalFailed events.Type = "cert.renewal_failed"
)

var (
	// ErrDNS marks a failure while creating, verifying or deleting the
	// challenge record.
	ErrDNS = errors.New("dns challenge failed")
	// ErrStorage marks a failure reading or writing persisted state.
	ErrStorage = errors.New("certificate storage failed")
	// ErrInvalidGeneration marks a certificate/key pair that failed
	// validation before activation.
	ErrInvalidGeneration = errors.New("certificate generation is not usable")
)
