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

func writeNotifyConfig(t *testing.T, chatsFile, apiBase string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	document := map[string]any{
		"listen": ":8888",
		"notify": map[string]any{
			"telegram": map[string]any{
				"token":      "test-token",
				"chats_file": chatsFile,
				"api_base":   apiBase,
			},
		},
		"routes": []map[string]string{{"host": "app.localhost", "target": "http://localhost:9000"}},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNotifyPairLinksOneChatWithTheOneTimeCode(t *testing.T) {
	chatsPath := filepath.Join(t.TempDir(), "telegram-chats.json")
	codeChannel := make(chan string, 1)

	var mu sync.Mutex
	var updateCalls int
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
			return
		}
		mu.Lock()
		updateCalls++
		call := updateCalls
		mu.Unlock()
		text, chatID := "/pair ZZZZZZZZ", 999 // a wrong code first
		if call > 1 {
			select {
			case code := <-codeChannel:
				text, chatID = "/pair "+code, 4242
			default:
				_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
				return
			}
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":` + strconv.Itoa(call) + `,"message":{"text":"` + text + `","chat":{"id":` + strconv.Itoa(chatID) + `}}}]}`))
	}))
	defer bot.Close()

	configPath := writeNotifyConfig(t, chatsPath, bot.URL)
	out := &syncBuffer{}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() {
		done <- runNotify(ctx, []string{"pair", "-config", configPath, "-timeout", "10s"}, out)
	}()

	// The code is only in the command's own output: the test digs it out the
	// way the operator reads it from the terminal.
	codePattern := regexp.MustCompile(`/pair ([A-Z2-9]{8})`)
	deadline := time.Now().Add(5 * time.Second)
	var code string
	for code == "" && time.Now().Before(deadline) {
		if match := codePattern.FindStringSubmatch(out.String()); match != nil {
			code = match[1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	if code == "" {
		t.Fatalf("the pairing code never appeared in the output:\n%s", out.String())
	}
	codeChannel <- code

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

	// Only the chat that used the real code is stored.
	data, err := os.ReadFile(chatsPath)
	if err != nil {
		t.Fatalf("read chats file: %v", err)
	}
	if string(data) != "[4242]\n" {
		t.Fatalf("chats file = %q, want the correctly paired chat only", data)
	}
}

func TestNotifyPairRequiresTelegramConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"listen": ":8888", "routes": [{"host": "a.localhost", "target": "http://localhost:9"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runNotify(context.Background(), []string{"pair", "-config", path}, &syncBuffer{})
	if err == nil || !strings.Contains(err.Error(), "notify.telegram is not configured") {
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
}
