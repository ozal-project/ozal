// Package notify carries Ozal's ChatOps pieces (NTF-001): the
// Notifier port, the alert bridge from events to messages, the one-time
// pairing code and the signed callback data. Technology adapters live in
// subpackages; telegram is the first one.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/ozal-project/ozal/internal/events"
)

// Message is one outbound notification.
type Message struct {
	ChatID int64
	Text   string
}

// Notifier delivers one message to one chat. Implementations bound their own
// timeouts and retries; a failure is reported, never retried forever.
type Notifier interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

// Chats resolves the paired chat IDs that alerts go to.
type Chats interface {
	IDs() []int64
}

// alertQueue bounds how many alerts may wait for delivery. The queue exists
// so Emit can never block: an access decision must not wait for Telegram.
const alertQueue = 32

// AlertSink turns selected events into chat messages. It implements
// events.Sink: Emit queues and returns immediately; one worker delivers in
// order with the notifier's own bounded retries. A full queue drops the
// alert and counts it, because losing a notification is better than slowing
// down the request path it describes.
type AlertSink struct {
	notifier Notifier
	chats    Chats
	logger   *slog.Logger
	queue    chan Message
	dropped  atomic.Int64
}

// NewAlertSink starts the delivery worker; it stops when ctx is cancelled.
func NewAlertSink(ctx context.Context, notifier Notifier, chats Chats, logger *slog.Logger) *AlertSink {
	if logger == nil {
		logger = slog.Default()
	}
	sink := &AlertSink{notifier: notifier, chats: chats, logger: logger, queue: make(chan Message, alertQueue)}
	go sink.run(ctx)
	return sink
}

// Emit queues one message per paired chat for the event types the tracker
// calls "logins and admin changes". Everything else stays in the event log.
func (s *AlertSink) Emit(e events.Event) {
	text, ok := alertText(e)
	if !ok {
		return
	}
	for _, chatID := range s.chats.IDs() {
		select {
		case s.queue <- Message{ChatID: chatID, Text: text}:
		default:
			s.dropped.Add(1)
			s.logger.Warn("alert queue is full; dropped a notification", "type", e.Type)
		}
	}
}

// Dropped reports how many alerts were lost to a full queue.
func (s *AlertSink) Dropped() int64 { return s.dropped.Load() }

func (s *AlertSink) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-s.queue:
			if err := s.notifier.SendMessage(ctx, message.ChatID, message.Text); err != nil {
				s.logger.Warn("notification could not be delivered", "chat", message.ChatID, "failure_class", "delivery")
			}
		}
	}
}

// alertTypes are the events that become chat messages.
func alertText(e events.Event) (string, bool) {
	switch e.Type {
	case events.LoginSucceeded,
		events.LoginFailed,
		events.RouteCreated,
		events.RouteCreateFailed,
		events.RouteDeleted,
		events.RouteDeleteFailed,
		events.ConfigReloaded,
		events.ConfigReloadFailed,
		events.PasskeyRegistered,
		// Every chat already paired learns that another one now gets alerts.
		events.NotifyChatPaired:
	default:
		return "", false
	}
	return "Ozal: " + eventLine(e), true
}

// eventLine lists only fields that are safe in a chat: never the error text
// or the reason, which can carry internals a chat does not need.
func eventLine(e events.Event) string {
	parts := []string{string(e.Type)}
	for _, field := range []struct{ key, value string }{
		{"actor", e.Actor},
		{"host", e.Host},
		{"code", e.Code},
	} {
		if field.value != "" {
			parts = append(parts, field.key+"="+field.value)
		}
	}
	return strings.Join(parts, " ")
}

// PairingText is what the operator sees; the code itself is printed by the
// caller so the message shape stays in one place.
func PairingText(code string) string {
	return fmt.Sprintf("Ozal: send /pair %s to the bot in a private chat to link it.", code)
}
