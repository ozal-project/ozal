package notify

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/events"
)

func TestPairingCodesAreTypableAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := NewPairingCode()
		if err != nil {
			t.Fatalf("NewPairingCode: %v", err)
		}
		if len(code) != pairingCodeLen {
			t.Fatalf("code %q has length %d", code, len(code))
		}
		for _, r := range code {
			if !strings.ContainsRune(pairingAlphabet, r) {
				t.Fatalf("code %q uses %q, which is not in the alphabet", code, r)
			}
		}
		seen[code] = true
	}
	if len(seen) < 190 {
		t.Fatalf("only %d distinct codes in 200 draws", len(seen))
	}
}

func TestPairCommand(t *testing.T) {
	valid := "ABC23456"
	tests := []struct {
		text string
		want string
		ok   bool
	}{
		{"/pair " + valid, valid, true},
		{"/pair " + strings.ToLower(valid), valid, true},
		{"/pair@ozal_bot " + valid, valid, true},
		{"/pair", "", false},
		{"/pair " + valid + " extra", "", false},
		{"pair " + valid, "", false},
		{"/other " + valid, "", false},
		{"/pair ABC", "", false},              // too short
		{"/pair ABC2345I", "", false},         // lookalike symbol is not in the alphabet
		{"/pair ABC23456ABC23456", "", false}, // too long
		{"hello", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := PairCommand(tt.text)
		if ok != tt.ok || got != tt.want {
			t.Errorf("PairCommand(%q) = (%q, %v), want (%q, %v)", tt.text, got, ok, tt.want, tt.ok)
		}
	}
	if !CodeEqual("ABC23456", "ABC23456") || CodeEqual("ABC23456", "ABC23457") || CodeEqual("short", "longer") {
		t.Fatal("CodeEqual does not compare the way a pairing check needs")
	}
}

func TestCallbackRoundTrip(t *testing.T) {
	key := CallbackKey("123456:test-token")
	now := time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)
	cb := Callback{Action: "approve", Nonce: "AB12CD34", ExpiresAt: now.Add(5 * time.Minute).Unix()}

	value, err := SignCallback(key, cb)
	if err != nil {
		t.Fatalf("SignCallback: %v", err)
	}
	if len(value) > CallbackMaxLen {
		t.Fatalf("signed data is %d bytes, Telegram allows %d", len(value), CallbackMaxLen)
	}
	got, err := VerifyCallback(key, value, now)
	if err != nil {
		t.Fatalf("VerifyCallback: %v", err)
	}
	if got != cb {
		t.Fatalf("verified %+v, want %+v", got, cb)
	}
}

func TestCallbackRefusals(t *testing.T) {
	key := CallbackKey("123456:test-token")
	now := time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)
	value, err := SignCallback(key, Callback{Action: "deny", Nonce: "AB12CD34", ExpiresAt: now.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("another key", func(t *testing.T) {
		if _, err := VerifyCallback(CallbackKey("other-token"), value, now); !errors.Is(err, ErrCallbackInvalid) {
			t.Fatalf("err = %v, want ErrCallbackInvalid", err)
		}
	})
	t.Run("tampered action", func(t *testing.T) {
		tampered := strings.Replace(value, "deny", "pass", 1)
		if _, err := VerifyCallback(key, tampered, now); !errors.Is(err, ErrCallbackInvalid) {
			t.Fatalf("err = %v, want ErrCallbackInvalid", err)
		}
	})
	t.Run("tampered tag", func(t *testing.T) {
		tampered := value[:len(value)-1] + "A"
		if _, err := VerifyCallback(key, tampered, now); !errors.Is(err, ErrCallbackInvalid) {
			t.Fatalf("err = %v, want ErrCallbackInvalid", err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		if _, err := VerifyCallback(key, value, now.Add(2*time.Minute)); !errors.Is(err, ErrCallbackExpired) {
			t.Fatalf("err = %v, want ErrCallbackExpired", err)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		for _, bad := range []string{"", "no-dot", "a.b", "x:y.zzz"} {
			if _, err := VerifyCallback(key, bad, now); !errors.Is(err, ErrCallbackInvalid) {
				t.Errorf("VerifyCallback(%q) err = %v, want ErrCallbackInvalid", bad, err)
			}
		}
	})
	t.Run("signing refuses what verification could not accept", func(t *testing.T) {
		for _, bad := range []Callback{
			{Action: "", Nonce: "N", ExpiresAt: 1},
			{Action: "Approve", Nonce: "N", ExpiresAt: 1},
			{Action: "approve", Nonce: "", ExpiresAt: 1},
			{Action: "approve", Nonce: "a:b", ExpiresAt: 1},
			{Action: "approve", Nonce: "N", ExpiresAt: 0},
			{Action: "approve", Nonce: strings.Repeat("A", 60), ExpiresAt: 1},
		} {
			if _, err := SignCallback(key, bad); !errors.Is(err, ErrCallbackInvalid) {
				t.Errorf("SignCallback(%+v) err = %v, want ErrCallbackInvalid", bad, err)
			}
		}
	})
}

func TestFileChatsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chats.json")
	chats, err := OpenFileChats(path)
	if err != nil {
		t.Fatalf("OpenFileChats: %v", err)
	}
	if len(chats.IDs()) != 0 {
		t.Fatal("a fresh store must have no chats")
	}
	if err := chats.Add(42); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := chats.Add(42); err != nil {
		t.Fatalf("Add duplicate: %v", err)
	}
	if err := chats.Add(7); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := chats.IDs(); len(got) != 2 || got[0] != 42 || got[1] != 7 {
		t.Fatalf("IDs = %v", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("chats file mode = %o, want 600", perm)
		}
	}

	// A chat paired by another process while this one runs is picked up when
	// the file changes.
	stamp := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[42,7,99]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := chats.IDs(); len(got) != 3 || got[2] != 99 {
		t.Fatalf("IDs after external change = %v", got)
	}

	// A corrupt file keeps the last good set instead of dropping alerts.
	if err := os.Chtimes(path, stamp.Add(time.Hour), stamp.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := chats.IDs(); len(got) != 3 {
		t.Fatalf("IDs after corruption = %v, want the last good set", got)
	}
}

type recordingNotifier struct {
	mu       sync.Mutex
	messages []Message
	block    chan struct{}
	started  chan struct{}
	err      error
}

func (n *recordingNotifier) SendMessage(ctx context.Context, chatID int64, text string) error {
	if n.started != nil {
		select {
		case n.started <- struct{}{}:
		default:
		}
	}
	if n.block != nil {
		select {
		case <-n.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if n.err != nil {
		return n.err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.messages = append(n.messages, Message{ChatID: chatID, Text: text})
	return nil
}

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func (n *recordingNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.messages)
}

type fixedChats []int64

func (c fixedChats) IDs() []int64 { return c }

func TestAlertSinkMapsLoginsAndAdminChanges(t *testing.T) {
	notifier := &recordingNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := NewAlertSink(ctx, notifier, fixedChats{1, 2}, nil)

	sink.Emit(events.Event{Type: events.LoginSucceeded, Actor: "user-1", Host: "app.example.com"})
	sink.Emit(events.Event{Type: events.RouteCreated, Actor: "admin", Host: "new.example.com"})
	sink.Emit(events.Event{
		Type: events.RouteCreateFailed, Actor: "admin", Host: "duplicate.example.com",
		Code: "route.already_exists", Error: "/private/config.json: secret-token",
	})
	sink.Emit(events.Event{Type: events.RouteDeleteFailed, Actor: "admin", Host: "missing.example.com", Code: "route.not_found"})
	sink.Emit(events.Event{Type: events.ConfigReloadFailed, Actor: "admin", Code: "config.reload_failed", Error: "/private/config.json: secret-token"})
	sink.Emit(events.Event{Type: events.AccessDenied, Actor: "user-2"}) // not an alert
	sink.Emit(events.Event{Type: events.AccessRateLimited})

	deadline := time.Now().Add(2 * time.Second)
	for notifier.count() < 10 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := notifier.count(); got != 10 {
		t.Fatalf("delivered %d messages, want 5 auth/admin events x 2 chats", got)
	}
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	var sawRouteCreateFailure, sawRouteDeleteFailure, sawReloadFailure bool
	for _, message := range notifier.messages {
		if !strings.Contains(message.Text, "Ozal:") || strings.Contains(message.Text, "reason") {
			t.Fatalf("message = %q", message.Text)
		}
		if strings.Contains(message.Text, "route.create_failed") {
			sawRouteCreateFailure = true
		}
		if strings.Contains(message.Text, "route.delete_failed") {
			sawRouteDeleteFailure = true
		}
		if strings.Contains(message.Text, "config.reload_failed") {
			sawReloadFailure = true
		}
		if strings.Contains(message.Text, "error=") || strings.Contains(message.Text, "/private/config.json") || strings.Contains(message.Text, "secret-token") {
			t.Fatalf("an alert must not leak raw error text: %q", message.Text)
		}
	}
	if !sawRouteCreateFailure || !sawRouteDeleteFailure || !sawReloadFailure {
		t.Fatalf("failure alerts missing: route-create=%v route-delete=%v reload=%v; messages=%+v", sawRouteCreateFailure, sawRouteDeleteFailure, sawReloadFailure, notifier.messages)
	}
}

func TestAlertSinkNeverBlocksTheEmitter(t *testing.T) {
	blocked := make(chan struct{})
	notifier := &recordingNotifier{block: blocked}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := NewAlertSink(ctx, notifier, fixedChats{1}, nil)

	// Let the worker pick up the first message and block inside the
	// notifier; the queue then fills and the flood must still return.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < alertQueue+10; i++ {
			sink.Emit(events.Event{Type: events.LoginFailed, Actor: "attacker"})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked the caller")
	}
	if sink.Dropped() == 0 {
		t.Fatal("a full queue must be counted as dropped alerts")
	}
	close(blocked)
	cancel()
}

func TestAlertSinkRedactsProviderErrorsFromLogs(t *testing.T) {
	const sentinel = "provider-secret-response"
	var output synchronizedBuffer
	notifier := &recordingNotifier{err: errors.New(sentinel)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := NewAlertSink(ctx, notifier, fixedChats{42}, slog.New(slog.NewJSONHandler(&output, nil)))
	sink.Emit(events.Event{Type: events.LoginFailed, Actor: "user"})

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(output.String(), "notification could not be delivered") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	got := output.String()
	if !strings.Contains(got, `"failure_class":"delivery"`) || !strings.Contains(got, `"chat":42`) {
		t.Fatalf("notification log lacks stable diagnostics: %s", got)
	}
	if strings.Contains(got, sentinel) {
		t.Fatalf("notification log leaked provider error: %s", got)
	}
}

func TestPairingText(t *testing.T) {
	if got := PairingText("ABC23456"); !strings.Contains(got, "/pair ABC23456") {
		t.Fatalf("PairingText = %q", got)
	}
}
