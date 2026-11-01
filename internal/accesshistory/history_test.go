package accesshistory

import (
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/events"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func decision(typ events.Type, actor, host, code string, min int) events.Event {
	return events.Event{Type: typ, Actor: actor, Host: host, Code: code, Time: at(min), RemoteAddr: "203.0.113.7"}
}

func TestStoreKeepsOnlyAccessDecisionsAndCountsThem(t *testing.T) {
	s := NewStore(Memory, t0, 10, 10)
	s.Add(decision(events.AccessAllowed, "sb:ana", "grafana.example.com", "allowed", 1))
	s.Add(decision(events.AccessAllowed, "sb:ana", "grafana.example.com", "allowed", 3))
	s.Add(decision(events.AccessDenied, "sb:bob", "grafana.example.com", "email_not_allowed", 2))
	s.Add(decision(events.AccessDenied, "", "grafana.example.com", "", 4))
	s.Add(decision(events.AccessStepUp, "sb:ana", "billing.example.com", "max_auth_age", 5))
	s.Add(decision(events.AccessRateLimited, "sb:bob", "grafana.example.com", "rate_limited", 6))
	s.Add(events.Event{Type: events.RouteCreated, Host: "wiki.example.com", Time: at(7)})

	sum := s.Summary(10)
	if sum.Allowed != 2 || sum.Refused != 2 || sum.StepUp != 1 || sum.RateLimited != 1 || sum.Held != 6 {
		t.Fatalf("summary = %+v", sum)
	}
	if !sum.Since.Equal(t0) || sum.Source != Memory {
		t.Fatalf("source/since = %s %s, want memory since start", sum.Source, sum.Since)
	}
	if len(sum.Reasons) != 2 || sum.Reasons[0].Count != 1 || sum.Reasons[0].Code != "email_not_allowed" || sum.Reasons[1].Code != "unspecified" {
		t.Fatalf("reasons = %+v", sum.Reasons)
	}
	if len(sum.People) != 1 || sum.People[0] != (Person{Actor: "sb:ana", Host: "grafana.example.com", Count: 2, Last: at(3)}) {
		t.Fatalf("people = %+v, want ana twice on grafana, last at the later decision", sum.People)
	}
}

func TestJournalStoreStartsAtItsFirstRecord(t *testing.T) {
	s := NewStore(Journal, t0.Add(48*time.Hour), 10, 10)
	if sum := s.Summary(5); !sum.Since.IsZero() {
		t.Fatalf("an empty journal has no start, got %s", sum.Since)
	}
	// The journal's first record is an admin change; access comes later.
	s.Add(events.Event{Type: events.AdminMutationRequested, Time: at(-90)})
	s.Add(decision(events.AccessAllowed, "sb:ana", "grafana.example.com", "allowed", 0))
	if sum := s.Summary(5); !sum.Since.Equal(at(-90)) || sum.Held != 1 {
		t.Fatalf("summary = %+v, want the journal's first record as the start", sum)
	}
}

func TestTotalsOutliveTheDetailedEntries(t *testing.T) {
	s := NewStore(Memory, t0, 4, 10)
	for i := range 10 {
		s.Add(decision(events.AccessDenied, "sb:bob", "grafana.example.com", "email_not_allowed", i))
	}
	sum := s.Summary(5)
	if sum.Refused != 10 || sum.Held != 4 || sum.OldestHeld == nil || !sum.OldestHeld.Equal(at(6)) {
		t.Fatalf("summary = %+v, want 10 refusals counted and the newest 4 held from minute 6", sum)
	}
	page := s.Query(Filter{Limit: 100})
	if len(page.Entries) != 4 || page.Entries[0].Time != at(9) || page.Entries[3].Time != at(6) {
		t.Fatalf("held entries = %+v, want minutes 9..6 newest first", page.Entries)
	}
}

func TestPeopleAreBoundedAndSaySo(t *testing.T) {
	s := NewStore(Memory, t0, 10, 2)
	for i, who := range []string{"sb:a", "sb:b", "sb:c"} {
		s.Add(decision(events.AccessAllowed, who, "grafana.example.com", "allowed", i))
	}
	sum := s.Summary(10)
	if sum.PeopleTotal != 2 || !sum.PeopleCapped || sum.Allowed != 3 {
		t.Fatalf("summary = %+v, want 2 tracked, capped, every decision counted", sum)
	}
	if sum := s.Summary(1); len(sum.People) != 1 || sum.People[0].Actor != "sb:b" {
		t.Fatalf("people = %+v, want only the most recent", sum.People)
	}
}

func TestQueryFiltersAndPagesNewestFirst(t *testing.T) {
	var entries []Entry
	for i := range 6 {
		outcome, actor := Allowed, "sb:ana"
		if i%2 == 1 {
			outcome, actor = Refused, "sb:bob"
		}
		entries = append(entries, Entry{Seq: uint64(i + 1), Time: at(i), Outcome: outcome, Actor: actor, Host: "grafana.example.com"})
	}
	page := Query(entries, Filter{Outcome: Refused, Limit: 2})
	if len(page.Entries) != 2 || page.Entries[0].Seq != 6 || page.Entries[1].Seq != 4 || page.Next != 4 {
		t.Fatalf("first page = %+v", page)
	}
	page = Query(entries, Filter{Outcome: Refused, Limit: 2, Before: page.Next})
	if len(page.Entries) != 1 || page.Entries[0].Seq != 2 || page.Next != 0 {
		t.Fatalf("second page = %+v, want the last refusal and no more", page)
	}
	if page := Query(entries, Filter{Actor: "sb:ana", Host: "other.example.com"}); len(page.Entries) != 0 {
		t.Fatalf("host filter ignored: %+v", page)
	}
	if page := Query(entries, Filter{Limit: MaxLimit + 50}); len(page.Entries) != 6 {
		t.Fatalf("all = %d entries", len(page.Entries))
	}
	if page := Query(nil, Filter{}); page.Entries == nil {
		t.Fatal("an empty page must be an empty list, not null")
	}
}

func TestQueryNeverReturnsMoreThanTheMaximum(t *testing.T) {
	s := NewStore(Memory, t0, 1000, 10)
	for i := range 300 {
		s.Add(decision(events.AccessAllowed, "sb:ana", "grafana.example.com", "allowed", i))
	}
	page := s.Query(Filter{Limit: 1000})
	if len(page.Entries) != MaxLimit || page.Next == 0 {
		t.Fatalf("page = %d entries next %d, want the maximum and a way to continue", len(page.Entries), page.Next)
	}
}
