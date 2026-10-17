package events

import "sync"

const (
	brokerHistory    = 200 // events replayed to a new subscriber
	subscriberBuffer = 64  // per-subscriber queue before events are dropped
	maxSubscribers   = 16
)

// Broker is a Sink that remembers the most recent events and fans new ones
// out to live subscribers. Emit never blocks: a subscriber whose queue is
// full loses events instead of slowing the request path.
type Broker struct {
	mu      sync.Mutex
	recent  []Event
	subs    map[chan Event]struct{}
	dropped uint64
}

func NewBroker() *Broker {
	return &Broker{subs: map[chan Event]struct{}{}}
}

func (b *Broker) Emit(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recent = append(b.recent, e)
	if len(b.recent) > brokerHistory {
		// Copy into a fresh slice so the old backing array can be collected.
		b.recent = append([]Event(nil), b.recent[len(b.recent)-brokerHistory:]...)
	}
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
			b.dropped++
		}
	}
}

// Subscribe returns the events so far and a channel of later ones. The caller
// must call cancel exactly when it stops reading; the channel is closed then.
// ok is false when too many subscribers are already attached.
//
// History and registration happen under one lock, so no event is missed or
// duplicated between the snapshot and the live channel.
func (b *Broker) Subscribe() (history []Event, live <-chan Event, cancel func(), ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs) >= maxSubscribers {
		return nil, nil, func() {}, false
	}
	ch := make(chan Event, subscriberBuffer)
	b.subs[ch] = struct{}{}
	history = append([]Event(nil), b.recent...)
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
