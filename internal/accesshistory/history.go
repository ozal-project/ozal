// Package accesshistory remembers who reached which protected application and
// why (UI-005). When access decisions are audited, its source is the durable
// journal: the store sees every record the journal held at startup and every
// record committed since, so the history survives restarts. Otherwise it holds
// only what this process saw, and says so.
//
// Queries and summaries are pure functions over entries; Store is the bounded,
// concurrency-safe holder that feeds them.
package accesshistory

import (
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/ozal-project/ozal/internal/events"
)

// Source names where the history comes from.
type Source string

const (
	// Journal: the durable audit journal, across restarts.
	Journal Source = "journal"
	// Memory: what this process saw since it started.
	Memory Source = "memory"
)

// Outcome is what a decision did to the request.
type Outcome string

const (
	Allowed     Outcome = "allowed"
	Refused     Outcome = "refused"
	StepUp      Outcome = "step_up"
	RateLimited Outcome = "rate_limited"
)

func outcomeOf(t events.Type) (Outcome, bool) {
	switch t {
	case events.AccessAllowed:
		return Allowed, true
	case events.AccessDenied:
		return Refused, true
	case events.AccessAuditFailed:
		return Refused, true
	case events.AccessStepUp:
		return StepUp, true
	case events.AccessRateLimited:
		return RateLimited, true
	}
	return "", false
}

// ValidOutcome reports whether value names an outcome.
func ValidOutcome(value Outcome) bool {
	return value == Allowed || value == Refused || value == StepUp || value == RateLimited
}

// Entry is one access decision. Seq orders entries within one store.
type Entry struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Outcome Outcome   `json:"outcome"`
	Host    string    `json:"host,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	Code    string    `json:"code,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Remote  string    `json:"remote,omitempty"`
}

// Filter selects entries. Empty fields match everything; Before pages
// backwards: only entries with a smaller Seq.
type Filter struct {
	Host    string
	Actor   string
	Outcome Outcome
	Before  uint64
	Limit   int
}

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Page is newest-first entries and, when older matches remain, the Before
// value that continues the listing.
type Page struct {
	Entries []Entry `json:"entries"`
	Next    uint64  `json:"next,omitempty"`
}

// Query selects from entries ordered oldest first. It never returns more than
// MaxLimit entries.
func Query(entries []Entry, f Filter) Page {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	page := Page{Entries: []Entry{}}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if f.Before != 0 && e.Seq >= f.Before {
			continue
		}
		if (f.Host != "" && e.Host != f.Host) || (f.Actor != "" && e.Actor != f.Actor) || (f.Outcome != "" && e.Outcome != f.Outcome) {
			continue
		}
		if len(page.Entries) == limit {
			page.Next = page.Entries[limit-1].Seq
			break
		}
		page.Entries = append(page.Entries, e)
	}
	return page
}

// Reason counts refusals with one decision code.
type Reason struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// Person is someone who got in to one application.
type Person struct {
	Actor string    `json:"actor"`
	Host  string    `json:"host"`
	Count int       `json:"count"`
	Last  time.Time `json:"last"`
}

// Summary covers every decision the store has seen, not only the entries it
// still holds in detail.
type Summary struct {
	Source Source `json:"source"`
	// Since is where the history starts: the journal's first record, or
	// when this process started.
	Since       time.Time `json:"since"`
	Allowed     int       `json:"allowed"`
	Refused     int       `json:"refused"`
	StepUp      int       `json:"step_up"`
	RateLimited int       `json:"rate_limited"`
	Reasons     []Reason  `json:"reasons"`
	// People got in, most recent first, at most the requested number.
	People []Person `json:"people"`
	// PeopleTotal counts every person-application pair seen; PeopleCapped
	// says some pairs past the store's bound were not tracked.
	PeopleTotal  int  `json:"people_total"`
	PeopleCapped bool `json:"people_capped,omitempty"`
	// Held is how many decisions are kept in detail, the oldest at
	// OldestHeld; older ones count in the totals but are not listed.
	Held       int        `json:"held"`
	OldestHeld *time.Time `json:"oldest_held,omitempty"`
}

type personKey struct{ actor, host string }

// Store keeps the newest decisions in detail and running totals for all of
// them. It is a events.Sink, so in memory mode it is fed like any other sink.
type Store struct {
	mu        sync.Mutex
	source    Source
	since     time.Time
	capacity  int
	maxPeople int
	seq       uint64
	entries   []Entry
	totals    map[Outcome]int
	reasons   map[string]int
	people    map[personKey]*Person
	capped    bool
}

const (
	DefaultCapacity  = 5000
	DefaultMaxPeople = 10000
)

// NewStore holds up to capacity decisions in detail and tracks up to
// maxPeople person-application pairs. A journal store's start is the time of
// the first record it observes; a memory store starts at since.
func NewStore(source Source, since time.Time, capacity, maxPeople int) *Store {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	if maxPeople <= 0 {
		maxPeople = DefaultMaxPeople
	}
	if source == Journal {
		since = time.Time{}
	}
	return &Store{
		source: source, since: since.UTC(), capacity: capacity, maxPeople: maxPeople,
		totals: map[Outcome]int{}, reasons: map[string]int{}, people: map[personKey]*Person{},
	}
}

// Emit makes Store a events.Sink; events other than access decisions only
// mark where a journal starts.
func (s *Store) Emit(e events.Event) { s.Add(e) }

// Add records one event.
func (s *Store) Add(e events.Event) {
	at := e.Time.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.since.IsZero() {
		s.since = at
	}
	outcome, ok := outcomeOf(e.Type)
	if !ok {
		return
	}
	s.seq++
	s.entries = append(s.entries, Entry{
		Seq: s.seq, Time: at, Outcome: outcome, Host: e.Host, Actor: e.Actor,
		Code: e.Code, Reason: e.Reason, Remote: e.RemoteAddr,
	})
	// Trim in batches so a busy edge does not copy on every decision.
	if len(s.entries) > s.capacity+s.capacity/4 {
		s.entries = slices.Clone(s.entries[len(s.entries)-s.capacity:])
	}
	s.totals[outcome]++
	switch outcome {
	case Refused:
		code := e.Code
		if code == "" {
			code = "unspecified"
		}
		s.reasons[code]++
	case Allowed:
		if e.Actor == "" {
			return
		}
		key := personKey{e.Actor, e.Host}
		if p, ok := s.people[key]; ok {
			p.Count++
			if at.After(p.Last) {
				p.Last = at
			}
			return
		}
		if len(s.people) >= s.maxPeople {
			s.capped = true
			return
		}
		s.people[key] = &Person{Actor: e.Actor, Host: e.Host, Count: 1, Last: at}
	}
}

// held returns the detailed entries, oldest first, without the trimming slack.
func (s *Store) held() []Entry {
	if len(s.entries) > s.capacity {
		return s.entries[len(s.entries)-s.capacity:]
	}
	return s.entries
}

// Query lists held decisions.
func (s *Store) Query(f Filter) Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Query(s.held(), f)
}

// Summary describes everything seen, listing at most people persons.
func (s *Store) Summary(people int) Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.held()
	sum := Summary{
		Source: s.source, Since: s.since,
		Allowed: s.totals[Allowed], Refused: s.totals[Refused], StepUp: s.totals[StepUp], RateLimited: s.totals[RateLimited],
		Reasons: []Reason{}, People: []Person{}, PeopleTotal: len(s.people), PeopleCapped: s.capped, Held: len(held),
	}
	if len(held) > 0 {
		oldest := held[0].Time
		sum.OldestHeld = &oldest
	}
	for code, n := range s.reasons {
		sum.Reasons = append(sum.Reasons, Reason{Code: code, Count: n})
	}
	sort.Slice(sum.Reasons, func(i, j int) bool {
		a, b := sum.Reasons[i], sum.Reasons[j]
		return a.Count > b.Count || (a.Count == b.Count && a.Code < b.Code)
	})
	all := make([]Person, 0, len(s.people))
	for _, p := range s.people {
		all = append(all, *p)
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if !a.Last.Equal(b.Last) {
			return a.Last.After(b.Last)
		}
		return a.Actor+"\x00"+a.Host < b.Actor+"\x00"+b.Host
	})
	if people > 0 && len(all) > people {
		all = all[:people]
	}
	sum.People = append(sum.People, all...)
	return sum
}
