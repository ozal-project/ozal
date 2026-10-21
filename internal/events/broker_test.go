package events

import (
	"testing"
	"time"
)

func TestBrokerReplaysHistoryThenStreams(t *testing.T) {
	b := NewBroker()
	b.Emit(Event{Type: LoginStarted})
	history, live, cancel, ok := b.Subscribe(0)
	if !ok {
		t.Fatal("subscribe refused")
	}
	defer cancel()
	if len(history) != 1 || history[0].Type != LoginStarted {
		t.Fatalf("history = %v", history)
	}
	b.Emit(Event{Type: AccessDenied})
	select {
	case e := <-live:
		if e.Type != AccessDenied {
			t.Fatalf("live event = %v", e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no live event")
	}
}

func TestBrokerSlowSubscriberNeverBlocksEmit(t *testing.T) {
	b := NewBroker()
	_, _, cancel, _ := b.Subscribe(0) // never reads
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBuffer*3; i++ {
			b.Emit(Event{Type: AccessAllowed})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked on a slow subscriber")
	}
	if b.Dropped() == 0 {
		t.Fatal("expected dropped events to be counted")
	}
}

func TestBrokerHistoryIsBoundedAndCancelClosesChannel(t *testing.T) {
	b := NewBroker()
	for i := 0; i < brokerHistory+50; i++ {
		b.Emit(Event{Type: AccessAllowed})
	}
	history, live, cancel, _ := b.Subscribe(0)
	if len(history) != brokerHistory {
		t.Fatalf("history len = %d, want %d", len(history), brokerHistory)
	}
	cancel()
	cancel() // idempotent
	if _, open := <-live; open {
		t.Fatal("channel not closed after cancel")
	}
}

func TestBrokerCapsSubscribers(t *testing.T) {
	b := NewBroker()
	for i := 0; i < maxSubscribers; i++ {
		if _, _, _, ok := b.Subscribe(0); !ok {
			t.Fatalf("subscriber %d refused", i)
		}
	}
	if _, _, _, ok := b.Subscribe(0); ok {
		t.Fatal("subscriber over the cap accepted")
	}
}

// BUG-009: a reconnecting client asks for what came after the last event it
// saw, and gets only that.
func TestBrokerResumesAfterASequenceNumber(t *testing.T) {
	b := NewBroker()
	for _, typ := range []Type{LoginStarted, AccessDenied, AccessAllowed} {
		b.Emit(Event{Type: typ})
	}
	all, _, cancel, _ := b.Subscribe(0)
	cancel()
	if len(all) != 3 || all[0].Seq != 1 || all[2].Seq != 3 {
		t.Fatalf("history = %+v, want seq 1..3", all)
	}
	after, _, cancel, _ := b.Subscribe(2)
	cancel()
	if len(after) != 1 || after[0].Seq != 3 || after[0].Type != AccessAllowed {
		t.Fatalf("after 2 = %+v, want only seq 3", after)
	}
	if none, _, cancel, _ := b.Subscribe(3); len(none) != 0 {
		cancel()
		t.Fatalf("after the last one = %+v, want nothing", none)
	} else {
		cancel()
	}
}

func TestEachBrokerHasItsOwnEpoch(t *testing.T) {
	a, b := NewBroker(), NewBroker()
	if a.Epoch() == "" || a.Epoch() == b.Epoch() {
		t.Fatalf("epochs %q and %q: a restart must not reuse event identities", a.Epoch(), b.Epoch())
	}
}
