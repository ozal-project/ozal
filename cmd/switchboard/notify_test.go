package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/admin"
	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/notify"
	"github.com/switchboard-project/switchboard/internal/notify/telegram"
)

// syncBuffer is a writer the test can read while the command under test is
// still writing to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// fakeBot answers getUpdates with whatever next() returns (nil: nothing
// new) and accepts every other method.
func fakeBot(t *testing.T, next func(call int) (text string, chatID int, ok bool)) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	var calls int
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
			return
		}
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		text, chatID, ok := next(call)
		if !ok {
			time.Sleep(10 * time.Millisecond) // a long poll that found nothing
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":` + strconv.Itoa(call) + `,"message":{"text":"` + text + `","chat":{"id":` + strconv.Itoa(chatID) + `}}}]}`))
	}))
	t.Cleanup(bot.Close)
	return bot
}

type pairingAdmin struct {
	config   string
	chats    string
	recorder *events.Recorder
	pairer   *notify.Pairer
}

// startPairingAdmin runs the real admin handler with a Pairer on a unix
// socket, as the process does when notify.telegram is configured.
func startPairingAdmin(t *testing.T, botURL string) pairingAdmin {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dir := t.TempDir()
	chatsPath := filepath.Join(dir, "telegram-chats.json")
	client, err := telegram.New(telegram.Config{Token: "test-token", BaseURL: botURL})
	if err != nil {
		t.Fatal(err)
	}
	chats, err := notify.OpenFileChats(chatsPath)
	if err != nil {
		t.Fatal(err)
	}
	pairer := notify.NewPairer(ctx, client.Inbox(0), chats, nil)

	socket := socketPath(t)
	raw, _ := json.Marshal(map[string]any{
		"listen": "127.0.0.1:8080",
		"admin":  map[string]string{"socket": socket},
		"routes": []map[string]string{{"host": "app.localhost", "target": "http://127.0.0.1:9000"}},
	})
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := &events.Recorder{}
	handler := admin.New(staticManager{config.Config{Listen: "127.0.0.1:8080"}}, recorder, admin.WithPairing(pairer))
	listener, err := listenAdminSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ConnContext: admin.LocalConn}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })

	previous := pairPoll
	pairPoll = 5 * time.Millisecond
	t.Cleanup(func() { pairPoll = previous })
	return pairingAdmin{config: configPath, chats: chatsPath, recorder: recorder, pairer: pairer}
}

// waitForCode digs the code out of the command's output, the way the
// operator reads it from the terminal.
func waitForCode(t *testing.T, out *syncBuffer) string {
	t.Helper()
	pattern := regexp.MustCompile(`/pair ([A-Z2-9]{8})`)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if match := pattern.FindStringSubmatch(out.String()); match != nil {
			return match[1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the pairing code never appeared in the output:\n%s", out.String())
	return ""
}

func TestNotifyPairLinksOneChatThroughTheProcess(t *testing.T) {
	codes := make(chan string, 1)
	bot := fakeBot(t, func(call int) (string, int, bool) {
		if call == 1 {
			return "/pair ZZZZZZZZ", 999, true // a wrong code first
		}
		select {
		case code := <-codes:
			return "/pair " + code, 4242, true
		default:
			return "", 0, false
		}
	})
	live := startPairingAdmin(t, bot.URL)

	out := &syncBuffer{}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { done <- runNotify(ctx, []string{"pair", "-config", live.config, "-timeout", "10s"}, out) }()
	codes <- waitForCode(t, out)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("notify pair: %v\noutput:\n%s", err, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("notify pair did not finish\noutput:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Paired chat 4242") {
		t.Fatalf("output does not confirm the pairing:\n%s", out.String())
	}
	// Only the chat that used the real code is stored, by the process.
	data, err := os.ReadFile(live.chats)
	if err != nil {
		t.Fatalf("read chats file: %v", err)
	}
	if string(data) != "[4242]\n" {
		t.Fatalf("chats file = %q, want the correctly paired chat only", data)
	}
	got := live.recorder.Events()
	if len(got) != 1 || got[0].Type != events.NotifyChatPaired || got[0].Actor != "local" || got[0].Code != "chat:4242" {
		t.Fatalf("events = %+v, want one notify.chat_paired by local for chat:4242", got)
	}
	if strings.Contains(got[0].Reason+got[0].Code+got[0].Host, waitForCode(t, out)) {
		t.Fatal("the pairing code must never be part of an event")
	}
}

func TestNotifyPairInterruptedCancelsTheCode(t *testing.T) {
	bot := fakeBot(t, func(int) (string, int, bool) { return "", 0, false })
	live := startPairingAdmin(t, bot.URL)

	out := &syncBuffer{}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- runNotify(ctx, []string{"pair", "-config", live.config}, out) }()
	waitForCode(t, out)
	cancel()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("err = %v, want the interruption to cancel the pairing", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notify pair did not stop when interrupted")
	}
	if state := live.pairer.Status().State; state != notify.PairingCancelled {
		t.Fatalf("pairing state = %q, want cancelled", state)
	}
	if got := live.recorder.Events(); len(got) != 0 {
		t.Fatalf("events = %+v, want none", got)
	}
}

func TestNotifyPairNeedsTelegramOnTheEdge(t *testing.T) {
	live := startLiveAdmin(t) // no notify.telegram: no pairing endpoints
	err := runNotify(context.Background(), []string{"pair", "-config", live.config}, &syncBuffer{})
	if err == nil || !strings.Contains(err.Error(), "no notify.telegram configured") {
		t.Fatalf("err = %v", err)
	}
}

func TestNotifyUsage(t *testing.T) {
	if err := runNotify(context.Background(), nil, &syncBuffer{}); err == nil {
		t.Fatal("the command with no subcommand must fail")
	}
	if err := runNotify(context.Background(), []string{"send"}, &syncBuffer{}); err == nil {
		t.Fatal("an unknown subcommand must fail")
	}
	if err := runNotify(context.Background(), []string{"pair", "-timeout", "2h"}, &syncBuffer{}); err == nil {
		t.Fatal("a timeout above one hour must fail")
	}
}
