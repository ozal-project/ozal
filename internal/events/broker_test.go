package events

import (
	"testing"
	"time"
)

func TestBrokerReplaysHistoryThenStreams(t *testing.T) {
	b := NewBroker()
	b.Emit(Event{Type: LoginStarted})
	history, live, cancel, ok := b.Subscribe()
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
	_, _, cancel, _ := b.Subscribe() // never reads
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
	history, live, cancel, _ := b.Subscribe()
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
		if _, _, _, ok := b.Subscribe(); !ok {
			t.Fatalf("subscriber %d refused", i)
		}
	}
	if _, _, _, ok := b.Subscribe(); ok {
		t.Fatal("subscriber over the cap accepted")
	}
}
