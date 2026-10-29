package certs

import (
	"context"
	"errors"
	"fmt"
)

// TXTPublisher is what the built-in DNS adapter needs from the in-process
// authoritative server. It is declared here, by the consumer.
type TXTPublisher interface {
	Publish(name, value string) (id string, err error)
	Retract(id string) error
}

// BuiltinDNS publishes challenge records through Ozal's own authoritative
// server (internal/acmedns). It implements DNSProvider with no network call
// and no credential: a create either happens in memory or fails, so its
// outcome is never ambiguous.
type BuiltinDNS struct{ pub TXTPublisher }

// NewBuiltinDNS wraps a publisher.
func NewBuiltinDNS(pub TXTPublisher) (*BuiltinDNS, error) {
	if pub == nil {
		return nil, errors.New("certs: the built-in DNS adapter needs a publisher")
	}
	return &BuiltinDNS{pub: pub}, nil
}

// CreateTXT publishes the record and returns its exact identity.
func (b *BuiltinDNS) CreateTXT(_ context.Context, name, value string) (DNSRecord, error) {
	id, err := b.pub.Publish(name, value)
	if err != nil {
		return DNSRecord{}, fmt.Errorf("%w: built-in dns: %v", ErrDNS, err)
	}
	return DNSRecord{ZoneID: "builtin", ID: id, Name: name, Value: value}, nil
}

// DeleteTXT retracts exactly the record CreateTXT returned.
func (b *BuiltinDNS) DeleteTXT(_ context.Context, record DNSRecord) error {
	if record.ZoneID != "builtin" || record.ID == "" {
		return fmt.Errorf("%w: refusing to delete an unidentified record", ErrDNS)
	}
	return b.pub.Retract(record.ID)
}

var _ DNSProvider = (*BuiltinDNS)(nil)
