package certs

import (
	"context"
	"time"
)

// StoredGeneration is one complete certificate/key pair as it is persisted:
// the leaf plus any intermediates the CA returned, and the private key. The
// two halves travel as one unit, so a crash can never pair a certificate
// with the key of a different generation.
type StoredGeneration struct {
	CertPEM []byte
	KeyPEM  []byte
}

// Store persists complete certificate generations for a host. The manager
// consumes this interface; FileStore is the file-backed adapter.
type Store interface {
	// Load returns the newest complete generation. ok is false when none
	// exists yet — a fresh installation, not an error.
	Load(ctx context.Context, host string) (gen StoredGeneration, ok bool, err error)
	// Save stores a complete generation durably and activates it. Once Save
	// returns nil, a later Load must return this generation even after a
	// crash, and an interrupted Save must never activate anything.
	Save(ctx context.Context, host string, gen StoredGeneration) error
}

// ACMEAccount is the persisted registration at one ACME directory. The
// account key is separate from every certificate key, and both the identity
// and the key are kept per directory so staging and production never share
// state.
type ACMEAccount struct {
	DirectoryURL string `json:"directory_url"`
	Email        string `json:"email"`
	KID          string `json:"kid"`
	KeyPEM       []byte `json:"key_pem"`
}

// DNSIntent records one challenge TXT record this process asked the DNS
// provider to create. It is written before the create call returns, so it
// survives a crash between the API call and its response. RecordID stays
// empty while the create is ambiguous: the record may or may not exist, and
// cleanup must never guess. Recovery deletes by the exact owned ID only.
type DNSIntent struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	ZoneID    string    `json:"zone_id,omitempty"`
	RecordID  string    `json:"record_id,omitempty"`
	Ambiguous bool      `json:"ambiguous,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
