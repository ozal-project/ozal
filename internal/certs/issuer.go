package certs

import (
	"context"
	"crypto"
	"errors"
	"time"
)

// Issuer completes one DNS-01 issuance. The manager is the only caller and
// serializes attempts, so an implementation may assume one call at a time.
// Recovery is separate: it retries cleanup left behind by earlier runs.
type Issuer interface {
	// Obtain acquires a certificate for host whose private key is key, and
	// returns the complete pem-encoded generation.
	Obtain(ctx context.Context, host string, key crypto.Signer) (StoredGeneration, error)
	// Recover retries cleanup of DNS records earlier runs left behind and
	// reports how many still need attention (0 when everything was
	// reclaimed or nothing was pending).
	Recover(ctx context.Context) (pending int, err error)
}

// DNSRecord identifies the exact record a provider created. Cleanup deletes
// by ID within its zone; deleting by name is deliberately impossible.
type DNSRecord struct {
	ZoneID string
	ID     string
	Name   string
	Value  string
}

// DNSProvider creates and deletes challenge TXT records. It is declared
// here, by the consumer, and implemented by the Cloudflare adapter.
type DNSProvider interface {
	CreateTXT(ctx context.Context, name, value string) (DNSRecord, error)
	DeleteTXT(ctx context.Context, record DNSRecord) error
}

// TXTResolver looks up TXT records. *net.Resolver satisfies it; tests use a
// fake so no test ever depends on public DNS.
type TXTResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// ErrAmbiguous marks a DNS API call whose outcome is unknown: the request
// may or may not have been applied. Cleanup never guesses on an ambiguous
// create — it records the intent and leaves the record for recovery or an
// operator.
var ErrAmbiguous = errors.New("dns api outcome is unknown")

// RateLimitedError reports that the ACME server asked to wait before the
// next attempt (RFC 8555 section 6.6). RetryAfter is the server's own
// figure; the manager honors it instead of its own backoff.
type RateLimitedError struct {
	RetryAfter time.Duration
	Err        error
}

func (e *RateLimitedError) Error() string {
	return e.Err.Error() + " (rate limited)"
}

func (e *RateLimitedError) Unwrap() error { return e.Err }
