package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBot is a simulated Bot API: it records requests and answers from a
// script, so no test ever talks to Telegram or waits on a real backoff.
type fakeBot struct {
	mu       sync.Mutex
	requests []fakeRequest
	script   []scriptedAnswer

	server *httptest.Server
}

type fakeRequest struct {
	path string
	body []byte
}

type scriptedAnswer struct {
	status int
	body   string
}

func newFakeBot(t *testing.T, script ...scriptedAnswer) *fakeBot {
	t.Helper()
	bot := &fakeBot{script: script}
	bot.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bot.mu.Lock()
		bot.requests = append(bot.requests, fakeRequest{path: r.URL.Path, body: body})
		var answer scriptedAnswer
		if len(bot.script) > 0 {
			answer = bot.script[0]
			bot.script = bot.script[1:]
		} else {
			answer = scriptedAnswer{status: http.StatusOK, body: `{"ok":true,"result":{}}`}
		}
		bot.mu.Unlock()
		if answer.status != 0 {
			w.WriteHeader(answer.status)
		}
		_, _ = w.Write([]byte(answer.body))
	}))
	t.Cleanup(bot.server.Close)
	return bot
}

func (b *fakeBot) captured() []fakeRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]fakeRequest(nil), b.requests...)
}

func testClient(t *testing.T, bot *fakeBot, cfg Config) *Client {
	t.Helper()
	cfg.Token = "123456:secret-token"
	cfg.BaseURL = bot.server.URL
	cfg.HTTPClient = bot.server.Client()
	if cfg.RetryBase == 0 {
		cfg.RetryBase = time.Millisecond
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestTokenIsRequired(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("an empty token must fail at composition time")
	}
}

func TestSendMessage(t *testing.T) {
	bot := newFakeBot(t)
	client := testClient(t, bot, Config{})
	if err := client.SendMessage(context.Background(), 42, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	requests := bot.captured()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if requests[0].path != "/bot123456:secret-token/sendMessage" {
		t.Fatalf("path = %q", requests[0].path)
	}
	var body map[string]any
	if err := json.Unmarshal(requests[0].body, &body); err != nil {
		t.Fatal(err)
	}
	if body["chat_id"] != float64(42) || body["text"] != "hello" {
		t.Fatalf("body = %v", body)
	}
}

func TestSendMessageRefusesEmptyText(t *testing.T) {
	bot := newFakeBot(t)
	client := testClient(t, bot, Config{})
	if err := client.SendMessage(context.Background(), 1, "   "); err == nil {
		t.Fatal("an empty message must not be sent")
	}
	if got := len(bot.captured()); got != 0 {
		t.Fatalf("%d requests reached the API", got)
	}
}

func TestSendMessageRetriesBoundedly(t *testing.T) {
	t.Run("429 then success honors retry after", func(t *testing.T) {
		bot := newFakeBot(t,
			scriptedAnswer{status: http.StatusTooManyRequests, body: `{"ok":false,"description":"slow down","parameters":{"retry_after":0}}`},
			scriptedAnswer{status: http.StatusOK, body: `{"ok":true,"result":{}}`},
		)
		client := testClient(t, bot, Config{})
		if err := client.SendMessage(context.Background(), 1, "hello"); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
		if got := len(bot.captured()); got != 2 {
			t.Fatalf("requests = %d, want one retry", got)
		}
	})
	t.Run("5xx then success", func(t *testing.T) {
		bot := newFakeBot(t,
			scriptedAnswer{status: http.StatusBadGateway, body: `{"ok":false,"description":"bad gateway"}`},
			scriptedAnswer{status: http.StatusOK, body: `{"ok":true,"result":{}}`},
		)
		client := testClient(t, bot, Config{})
		if err := client.SendMessage(context.Background(), 1, "hello"); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	})
	t.Run("4xx is definite", func(t *testing.T) {
		bot := newFakeBot(t,
			scriptedAnswer{status: http.StatusBadRequest, body: `{"ok":false,"description":"chat not found"}`},
		)
		client := testClient(t, bot, Config{})
		err := client.SendMessage(context.Background(), 1, "hello")
		var apiErr *APIError
		if err == nil || !errors.As(err, &apiErr) || apiErr.Description != "chat not found" {
			t.Fatalf("err = %v", err)
		}
		if got := len(bot.captured()); got != 1 {
			t.Fatalf("requests = %d, want no retry for a client error", got)
		}
	})
	t.Run("retries are exhausted", func(t *testing.T) {
		bot := newFakeBot(t,
			scriptedAnswer{status: http.StatusInternalServerError, body: `{"ok":false,"description":"boom"}`},
			scriptedAnswer{status: http.StatusInternalServerError, body: `{"ok":false,"description":"boom"}`},
		)
		client := testClient(t, bot, Config{Retries: 1})
		if err := client.SendMessage(context.Background(), 1, "hello"); err == nil {
			t.Fatal("a persistent server error must surface")
		}
		if got := len(bot.captured()); got != 2 {
			t.Fatalf("requests = %d, want exactly one retry", got)
		}
	})
}

func TestTransportErrorsDoNotLeakTheToken(t *testing.T) {
	bot := newFakeBot(t)
	client := testClient(t, bot, Config{Retries: 1})
	bot.server.Close() // every request now fails at the transport
	err := client.SendMessage(context.Background(), 1, "hello")
	if err == nil {
		t.Fatal("SendMessage must fail when the API is unreachable")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("the error leaks the token: %v", err)
	}
}

func TestGetUpdates(t *testing.T) {
	bot := newFakeBot(t,
		scriptedAnswer{status: http.StatusOK, body: `{"ok":true,"result":[{"update_id":7,"message":{"text":"/pair ABC23456","chat":{"id":42}}}]}`},
	)
	client := testClient(t, bot, Config{})
	updates, err := client.GetUpdates(context.Background(), 5, 3*time.Second)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(updates) != 1 || updates[0].UpdateID != 7 || updates[0].Message == nil ||
		updates[0].Message.Text != "/pair ABC23456" || updates[0].Message.Chat.ID != 42 {
		t.Fatalf("updates = %+v", updates)
	}
	var body map[string]any
	if err := json.Unmarshal(bot.captured()[0].body, &body); err != nil {
		t.Fatal(err)
	}
	if body["offset"] != float64(5) || body["timeout"] != float64(3) {
		t.Fatalf("getUpdates body = %v", body)
	}
}
