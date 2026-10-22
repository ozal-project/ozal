package notify

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// chanInbox delivers what the test sends and counts concurrent readers:
// Telegram refuses a second getUpdates while one is open.
type chanInbox struct {
	messages chan []Incoming
	readers  atomic.Int32
	overlap  atomic.Bool
}

func newChanInbox() *chanInbox { return &chanInbox{messages: make(chan []Incoming)} }

func (in *chanInbox) Receive(ctx context.Context) ([]Incoming, error) {
	if in.readers.Add(1) > 1 {
		in.overlap.Store(true)
	}
	defer in.readers.Add(-1)
	select {
	case m := <-in.messages:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type memoryChats struct {
	mu  sync.Mutex
	ids []int64
	err error
}

func (c *memoryChats) Add(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.ids = append(c.ids, id)
	return nil
}

func waitState(t *testing.T, p *Pairer, want PairingState) PairingStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := p.Status(); s.State == want {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pairing state = %q, want %q", p.Status().State, want)
	return PairingStatus{}
}

func TestPairerRecordsOnlyTheChatWithTheCode(t *testing.T) {
	inbox, chats := newChanInbox(), &memoryChats{}
	p := NewPairer(context.Background(), inbox, chats, nil)
	paired := make(chan int64, 1)
	code, expires, err := p.Start(time.Minute, func(id int64) { paired <- id })
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(expires) <= 0 || p.Status().State != PairingWaiting {
		t.Fatalf("status = %+v", p.Status())
	}
	inbox.messages <- []Incoming{{ChatID: 1, Text: "/pair ZZZZZZZZ"}, {ChatID: 2, Text: "hello"}}
	inbox.messages <- []Incoming{{ChatID: 7, Text: "/pair " + code}}
	if id := <-paired; id != 7 {
		t.Fatalf("paired %d, want 7", id)
	}
	if s := waitState(t, p, PairingPaired); s.ChatID != 7 {
		t.Fatalf("status = %+v", s)
	}
	if len(chats.ids) != 1 || chats.ids[0] != 7 {
		t.Fatalf("chats = %v, want only 7", chats.ids)
	}
}

func TestPairerOneReaderAtATime(t *testing.T) {
	inbox := newChanInbox()
	p := NewPairer(context.Background(), inbox, &memoryChats{}, nil)
	if _, _, err := p.Start(time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Start(time.Minute, nil); !errors.Is(err, ErrPairingActive) {
		t.Fatalf("second Start err = %v, want ErrPairingActive", err)
	}
	if !p.Cancel() {
		t.Fatal("Cancel found nothing waiting")
	}
	if p.Cancel() {
		t.Fatal("a second Cancel must report nothing waiting")
	}
	waitState(t, p, PairingCancelled)
	// Once the cancelled reader has returned, a new pairing may start, and
	// it is not ended by the old one's cleanup.
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, _, err := p.Start(time.Minute, nil)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrPairingActive) || time.Now().After(deadline) {
			t.Fatalf("restart err = %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if s := p.Status(); s.State != PairingWaiting {
		t.Fatalf("new pairing state = %q, want waiting", s.State)
	}
	if inbox.overlap.Load() {
		t.Fatal("two pairings read the inbox at the same time")
	}
}

func TestPairerExpiresUnused(t *testing.T) {
	p := NewPairer(context.Background(), newChanInbox(), &memoryChats{}, nil)
	called := false
	if _, _, err := p.Start(20*time.Millisecond, func(int64) { called = true }); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, PairingExpired)
	if called {
		t.Fatal("an expired pairing must not report a chat")
	}
}

func TestPairerFailsWhenTheChatCannotBeRecorded(t *testing.T) {
	inbox := newChanInbox()
	p := NewPairer(context.Background(), inbox, &memoryChats{err: errors.New("disk full")}, nil)
	code, _, err := p.Start(time.Minute, func(int64) { t.Error("a chat that was not recorded must not be reported") })
	if err != nil {
		t.Fatal(err)
	}
	inbox.messages <- []Incoming{{ChatID: 7, Text: "/pair " + code}}
	waitState(t, p, PairingFailed)
}
