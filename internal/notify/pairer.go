package notify

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Incoming is one message a person sent to the bot.
type Incoming struct {
	ChatID int64
	Text   string
}

// Inbox yields the bot's incoming messages, each one once. Telegram allows
// a single reader of a bot's updates, so the process owns it (CLI-001):
// telegram.Client.Inbox adapts getUpdates.
type Inbox interface {
	Receive(ctx context.Context) ([]Incoming, error)
}

// ChatAdder records a paired chat (FileChats implements it).
type ChatAdder interface {
	Add(id int64) error
}

// ErrPairingActive refuses a second pairing while one still reads the inbox.
var ErrPairingActive = errors.New("notify: a pairing is already waiting")

// PairingState is where the latest pairing stands.
type PairingState string

const (
	PairingNone      PairingState = "none"
	PairingWaiting   PairingState = "waiting"
	PairingPaired    PairingState = "paired"
	PairingExpired   PairingState = "expired"
	PairingCancelled PairingState = "cancelled"
	PairingFailed    PairingState = "failed"
)

// PairingStatus describes the latest pairing. It never carries the code:
// only the caller of Start sees it.
type PairingStatus struct {
	State   PairingState
	ChatID  int64
	Expires time.Time
}

// Pairer links one chat at a time with a one-time code (NTF-001). The code
// lives only in this process and dies with its timeout; nothing is sent to
// the chat. A wrong code neither pairs nor ends the wait.
type Pairer struct {
	ctx    context.Context
	inbox  Inbox
	chats  ChatAdder
	logger *slog.Logger

	mu      sync.Mutex
	status  PairingStatus
	running bool
	cancel  context.CancelFunc
}

// NewPairer reads the inbox only while a pairing waits; ctx is the
// process's, so a pairing outlives the request that started it.
func NewPairer(ctx context.Context, inbox Inbox, chats ChatAdder, logger *slog.Logger) *Pairer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pairer{ctx: ctx, inbox: inbox, chats: chats, logger: logger, status: PairingStatus{State: PairingNone}}
}

// Start opens a pairing for ttl and returns its code. onPaired runs once,
// after the chat is recorded.
func (p *Pairer) Start(ttl time.Duration, onPaired func(chatID int64)) (string, time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// running, not the state: a cancelled pairing may still be inside a
	// long poll, and a second reader would make Telegram refuse both.
	if p.running {
		return "", time.Time{}, ErrPairingActive
	}
	code, err := NewPairingCode()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(ttl)
	ctx, cancel := context.WithDeadline(p.ctx, expires)
	p.status = PairingStatus{State: PairingWaiting, Expires: expires}
	p.running, p.cancel = true, cancel
	go p.wait(ctx, cancel, code, onPaired)
	return code, expires, nil
}

// Status reports the latest pairing.
func (p *Pairer) Status() PairingStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Cancel ends a waiting pairing; it reports whether one was waiting.
func (p *Pairer) Cancel() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status.State != PairingWaiting {
		return false
	}
	p.status.State = PairingCancelled
	p.cancel()
	return true
}

func (p *Pairer) wait(ctx context.Context, cancel context.CancelFunc, code string, onPaired func(int64)) {
	// Its own cancel: once running is false, p.cancel may be the next one's.
	defer cancel()
	chatID, failureClass, err := p.receive(ctx, code)
	p.mu.Lock()
	switch {
	case err == nil:
		p.status.State, p.status.ChatID = PairingPaired, chatID
	case p.status.State == PairingCancelled:
	case ctx.Err() != nil:
		p.status.State = PairingExpired
	default:
		p.status.State = PairingFailed
		p.logger.Warn("chat pairing failed", "failure_class", failureClass)
	}
	p.running = false
	p.mu.Unlock()
	if err == nil && onPaired != nil {
		onPaired(chatID)
	}
}

func (p *Pairer) receive(ctx context.Context, code string) (int64, string, error) {
	for {
		messages, err := p.inbox.Receive(ctx)
		if err != nil {
			return 0, "inbox", err
		}
		for _, message := range messages {
			supplied, ok := PairCommand(message.Text)
			if !ok || !CodeEqual(code, supplied) {
				continue
			}
			if err := p.chats.Add(message.ChatID); err != nil {
				return 0, "chat_store", err
			}
			return message.ChatID, "", nil
		}
	}
}
