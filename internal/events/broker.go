package events

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

const (
	brokerHistory    = 200 // events replayed to a new subscriber
	subscriberBuffer = 64  // per-subscriber queue before events are dropped
	maxSubscribers   = 16
)

// Broker is a Sink that remembers the most recent events and fans new ones
// out to live subscribers. Emit never blocks: a subscriber whose queue is
// full loses events instead of slowing the request path.
//
// Every event gets the next sequence number of this broker's epoch, a random
// value chosen per process (BUG-009). Together they identify an event, so a
// reconnecting client can ask only for what it has not seen, and spot what
// it missed by a jump in the sequence; a restart never reuses an identity.
type Broker struct {
	mu      sync.Mutex
	epoch   string
	seq     uint64
	recent  []Stamped
	subs    map[chan Stamped]struct{}
	dropped uint64
}

// Stamped is an event with its place in the broker's stream.
type Stamped struct {
	Seq uint64
	Event
}

func NewBroker() *Broker {
	var raw [8]byte
	_, _ = rand.Read(raw[:]) // crypto/rand.Read does not fail on supported platforms
	return &Broker{epoch: hex.EncodeToString(raw[:]), subs: map[chan Stamped]struct{}{}}
}

// Epoch names this broker's stream; sequence numbers count within it.
func (b *Broker) Epoch() string { return b.epoch }

func (b *Broker) Emit(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	stamped := Stamped{Seq: b.seq, Event: e}
	b.recent = append(b.recent, stamped)
	if len(b.recent) > brokerHistory {
		// Copy into a fresh slice so the old backing array can be collected.
		b.recent = append([]Stamped(nil), b.recent[len(b.recent)-brokerHistory:]...)
	}
	for ch := range b.subs {
		select {
		case ch <- stamped:
		default:
			b.dropped++
		}
	}
}

// Subscribe returns the retained events after sequence number after (0 for
// all of them) and a channel of later ones. The caller must call cancel
// exactly when it stops reading; the channel is closed then. ok is false when
// too many subscribers are already attached.
//
// History and registration happen under one lock, so no event is missed or
// duplicated between the snapshot and the live channel.
func (b *Broker) Subscribe(after uint64) (history []Stamped, live <-chan Stamped, cancel func(), ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs) >= maxSubscribers {
		return nil, nil, func() {}, false
	}
	ch := make(chan Stamped, subscriberBuffer)
	b.subs[ch] = struct{}{}
	for _, e := range b.recent {
		if e.Seq > after {
			history = append(history, e)
		}
	}
	var once sync.Once
	cancel = func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.subs, ch)
			close(ch)
		})
	}
	return history, ch, cancel, true
}

// Dropped counts events a slow subscriber missed.
func (b *Broker) Dropped() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// Multi forwards every event to all sinks, in order.
type Multi []Sink

func (m Multi) Emit(e Event) {
	for _, s := range m {
		s.Emit(e)
	}
}
