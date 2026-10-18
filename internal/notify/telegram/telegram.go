// Package telegram is the Telegram Bot API adapter for notify: the few
// methods the tracker requires (send a message, read updates for pairing),
// spoken with net/http and nothing else. The bot token is a credential: it
// travels in the request URL because the API demands it, and it is never
// copied into an error, a log line or an event.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the public Bot API.
	DefaultBaseURL = "https://api.telegram.org"
	// maxBody bounds what one API answer may read.
	maxBody = 1 << 20
	// DefaultClientTimeout caps a whole request; long polling sets its own
	// smaller context, so this is the outer bound of a stuck connection.
	DefaultClientTimeout = 60 * time.Second
	// MaxRetryAfter caps how long a rate limit may park the sender; the
	// manager-like bound keeps a hostile Retry-After from freezing alerts
	// for hours.
	MaxRetryAfter = 30 * time.Second
)

// Config configures the client. Token comes from a resolved config
// reference; BaseURL, HTTPClient, Retries and RetryBase exist so tests can
// simulate the API without waiting or touching the network.
type Config struct {
	Token      string
	BaseURL    string
	HTTPClient *http.Client
	// Retries is how many additional attempts a retryable failure gets;
	// zero selects 2.
	Retries int
	// RetryBase is the first backoff step; zero selects 500ms.
	RetryBase time.Duration
}

// Client speaks the Bot API methods this project uses.
type Client struct {
	token     string
	base      string
	http      *http.Client
	retries   int
	retryBase time.Duration
}

// New validates the credential and applies the defaults. An empty token
// fails here, at composition time, not on the first alert.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("notify/telegram: a bot token is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: DefaultClientTimeout}
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	}
	if cfg.Retries == 0 {
		cfg.Retries = 2
	}
	if cfg.RetryBase <= 0 {
		cfg.RetryBase = 500 * time.Millisecond
	}
	return &Client{
		token:     cfg.Token,
		base:      strings.TrimSuffix(cfg.BaseURL, "/"),
		http:      cfg.HTTPClient,
		retries:   cfg.Retries,
		retryBase: cfg.RetryBase,
	}, nil
}

// APIError is a refusal the Bot API returned. Description never contains the
// token: it is the server's own message.
type APIError struct {
	Method      string
	StatusCode  int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("notify/telegram: %s: status %d: %s", e.Method, e.StatusCode, e.Description)
}

// SendMessage delivers one message. 429 and 5xx answers are retried with a
// bounded backoff, honoring the server's retry_after; any other refusal is
// returned as it came.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("notify/telegram: refusing to send an empty message")
	}
	return c.call(ctx, "sendMessage", map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}, nil)
}

// Update is the subset of a Telegram update pairing reads.
type Update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

// GetUpdates long-polls for updates after offset. The caller's context must
// outlive poll; the client adds a small margin on top.
func (c *Client) GetUpdates(ctx context.Context, offset int64, poll time.Duration) ([]Update, error) {
	if poll < 0 {
		poll = 0
	}
	callCtx := ctx
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > poll+10*time.Second {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, poll+10*time.Second)
		defer cancel()
	}
	var updates []Update
	err := c.call(callCtx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(poll.Seconds()),
		"allowed_updates": []string{"message"},
	}, &updates)
	if err != nil {
		return nil, err
	}
	return updates, nil
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call performs one API method with bounded retries. Every error it returns
// is safe to log: transport errors are unwrapped past the URL that carries
// the token.
func (c *Client) call(ctx context.Context, method string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify/telegram: encode %s: %w", method, err)
	}
	for attempt := 0; ; attempt++ {
		res, err := c.do(ctx, method, body)
		if err == nil {
			switch {
			case res.payload.OK:
				if out != nil {
					if err := json.Unmarshal(res.payload.Result, out); err != nil {
						return fmt.Errorf("notify/telegram: %s: decode result: %w", method, err)
					}
				}
				return nil
			default:
				retryAfter := time.Duration(res.retryAfterSeconds()) * time.Second
				err = &APIError{Method: method, StatusCode: res.status, Description: res.description(), RetryAfter: retryAfter}
				if !retryable(res.status) {
					return err
				}
			}
		}
		if attempt >= c.retries {
			return err
		}
		wait := c.retryBase << min(attempt, 8)
		if apiErr, ok := err.(*APIError); ok && apiErr.RetryAfter > 0 {
			wait = apiErr.RetryAfter
		}
		if wait > MaxRetryAfter {
			wait = MaxRetryAfter
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type response struct {
	payload apiResponse
	status  int
	header  http.Header
}

func (r *response) description() string {
	if r.payload.Description != "" {
		return r.payload.Description
	}
	return http.StatusText(r.status)
}

func (r *response) retryAfterSeconds() int {
	if r.payload.Parameters != nil && r.payload.Parameters.RetryAfter > 0 {
		return r.payload.Parameters.RetryAfter
	}
	if seconds, err := strconv.Atoi(r.header.Get("Retry-After")); err == nil && seconds > 0 {
		return seconds
	}
	return 0
}

func (c *Client) do(ctx context.Context, method string, body []byte) (*response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("notify/telegram: %s: build request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// A *url.Error prints the request URL, which carries the token:
		// unwrap to the network cause before it reaches a log.
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("notify/telegram: %s: %w", method, err)
	}
	defer res.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(res.Body, maxBody))
	out := &response{status: res.StatusCode, header: res.Header}
	if readErr != nil {
		return nil, fmt.Errorf("notify/telegram: %s: read response: %w", method, readErr)
	}
	if err := json.Unmarshal(data, &out.payload); err != nil {
		return nil, &APIError{Method: method, StatusCode: res.StatusCode, Description: "unreadable API response"}
	}
	return out, nil
}

// retryable reports whether the status deserves another attempt: rate limits
// and server-side failures, never a client mistake.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}
