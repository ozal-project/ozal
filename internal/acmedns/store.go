package acmedns

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
)

// MaxRecords bounds the challenge records held at once. Issuance presents one
// at a time, so this only limits what a bug or an abuser could pile up.
const MaxRecords = 32

// Store holds the challenge TXT records the server publishes. It satisfies
// the publisher port of the certificate adapter: the record's ID is the only
// thing a retract needs, and retracting twice is not an error.
type Store struct {
	zone Zone

	mu      sync.Mutex
	next    uint64
	records map[string]record
}

type record struct{ name, value string }

// NewStore creates an empty store for a zone.
func NewStore(zone Zone) *Store {
	return &Store{zone: zone, records: map[string]record{}}
}

// Publish adds one TXT value at a name inside the zone and returns its ID.
func (s *Store) Publish(name, value string) (string, error) {
	name = clean(name)
	if !s.zone.Contains(name) || name == s.zone.name {
		return "", fmt.Errorf("acmedns: %s is not a name below the zone %s", name, s.zone.name)
	}
	if len(value) == 0 || len(value) > 255 {
		return "", errors.New("acmedns: a TXT value must be 1 to 255 bytes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.records) >= MaxRecords {
		return "", errors.New("acmedns: too many challenge records are published")
	}
	s.next++
	id := "acmedns-" + strconv.FormatUint(s.next, 10)
	s.records[id] = record{name: name, value: value}
	return id, nil
}

// Retract removes exactly the record Publish returned. An unknown ID counts
// as already removed.
func (s *Store) Retract(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
	return nil
}

// TXT returns the values published at a lower-case name, in a stable order.
func (s *Store) TXT(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.records {
		if r.name == name {
			out = append(out, r.value)
		}
	}
	sort.Strings(out)
	return out
}
