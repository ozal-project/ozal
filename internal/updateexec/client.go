package updateexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/updatejob"
)

// ErrNoJob means the executor has not run a job yet.
var ErrNoJob = errors.New("no update job has run")

// Error is a bounded error returned by the local executor.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// Client speaks only to the executor's Unix socket. It exposes the same fixed
// operations to the console API; no path, URL or command crosses it.
type Client struct {
	socket string
	http   *http.Client
}

func NewClient(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		DisableKeepAlives: true,
	}
	return &Client{socket: socket, http: &http.Client{Transport: transport, Timeout: 3 * time.Minute}}
}

func (c *Client) Plan(ctx context.Context) (updatejob.Plan, error) {
	var plan updatejob.Plan
	err := c.do(ctx, http.MethodGet, "/v1/plan", nil, &plan)
	return plan, err
}

func (c *Client) Start(ctx context.Context, planID string) (updatejob.Job, error) {
	var job updatejob.Job
	body := map[string]any{"plan_id": planID, "confirm": true}
	err := c.do(ctx, http.MethodPost, "/v1/jobs", body, &job)
	return job, err
}

func (c *Client) Job(ctx context.Context, id string) (updatejob.Job, error) {
	var job updatejob.Job
	err := c.do(ctx, http.MethodGet, "/v1/jobs/"+id, nil, &job)
	return job, err
}

func (c *Client) Latest(ctx context.Context) (updatejob.Job, error) {
	var job updatejob.Job
	err := c.do(ctx, http.MethodGet, "/v1/jobs/latest", nil, &job)
	if e, ok := err.(*Error); ok && e.Status == http.StatusNotFound {
		return updatejob.Job{}, ErrNoJob
	}
	return job, err
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://update-executor"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("the update executor is unavailable: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, updatejob.MaxJobBytes+1))
	if err != nil {
		return fmt.Errorf("read the update executor response: %w", err)
	}
	if len(raw) > updatejob.MaxJobBytes {
		return errors.New("the update executor response is too large")
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		message := strings.TrimSpace(failure.Error)
		if message == "" || len(message) > 200 {
			message = "the update executor refused the request"
		}
		return &Error{Status: res.StatusCode, Message: message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("read the update executor response: %w", err)
	}
	return nil
}
