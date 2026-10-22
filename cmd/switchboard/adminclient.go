package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
)

// adminClient reaches the admin API of the running process (CLI-001): the
// one door through which a change is validated, applied and published as
// an event. On the unix socket the file mode is the credential; over TCP
// the admin bearer is sent and host-only operations are refused.
type adminClient struct {
	client *http.Client
	base   string
	token  string
}

// adminFlags are the flags every API-backed command shares.
type adminFlags struct {
	target   *string
	tokenRef *string
	config   *string
}

func addAdminFlags(fs *flag.FlagSet) adminFlags {
	return adminFlags{
		target:   fs.String("admin", "", "admin API: unix:/path or http(s)://host:port (default: admin.socket from -config)"),
		tokenRef: fs.String("token", "env:SWITCHBOARD_ADMIN_TOKEN", "admin token reference for an http(s) -admin (env:NAME or file:/path)"),
		config:   fs.String("config", "config.json", "routing config (its admin.socket is the default -admin)"),
	}
}

func (f adminFlags) client() (*adminClient, error) {
	target := *f.target
	if target == "" {
		cfg, err := config.Load(*f.config)
		if err != nil {
			return nil, err
		}
		if cfg.Admin.Socket == "" {
			return nil, fmt.Errorf("%s has no admin.socket: set it, or pass -admin (or -db for offline break-glass)", *f.config)
		}
		target = "unix:" + cfg.Admin.Socket
	}
	if path, ok := strings.CutPrefix(target, "unix:"); ok {
		if path == "" {
			return nil, errors.New("-admin unix: needs a socket path")
		}
		return &adminClient{
			client: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", path)
				},
			}},
			// The admin handler answers loopback Host names only.
			base: "http://localhost",
		}, nil
	}
	base, err := adminBase(target)
	if err != nil {
		return nil, err
	}
	token, err := config.ResolveSecret(*f.tokenRef)
	if err != nil || token == "" {
		return nil, fmt.Errorf("admin token: set %s or pass -token file:/path", *f.tokenRef)
	}
	return &adminClient{client: &http.Client{Timeout: 30 * time.Second}, base: base, token: token}, nil
}

// do sends request (when non-nil) as JSON and decodes the answer into result.
func (c *adminClient) do(ctx context.Context, method, path string, request, result any) error {
	var body io.Reader
	contentType := ""
	if request != nil {
		encoded, err := json.Marshal(request)
		if err != nil {
			return err
		}
		body, contentType = bytes.NewReader(encoded), "application/json"
	}
	return call(ctx, c.client, method, c.base+path, c.token, contentType, body, result)
}

// apiError is a non-2xx answer from the admin API.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return e.Message }

func isStatus(err error, status int) bool {
	var api *apiError
	return errors.As(err, &api) && api.Status == status
}

// offlineNotice is printed by every -db command: it changes the state
// database behind the running process, so its event goes to stderr as the
// same JSON record the process would have logged.
func offlineNotice() events.Sink {
	fmt.Fprintln(os.Stderr, "offline (-db): bypasses the running process; this change will not appear in its activity feed or alerts")
	return events.SlogSink{Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil))}
}

func emitOffline(sink events.Sink, e events.Event) {
	e.Time, e.Actor = time.Now(), "offline"
	sink.Emit(e)
}
