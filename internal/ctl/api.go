package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SocketForwarder brings the edge's admin socket to the operator's machine
// as a private local unix socket.
type SocketForwarder interface {
	ForwardSocket(ctx context.Context, remote string) (local string, stop func(), err error)
}

// APIRequest is one `ozalctl api` call: a second path to everything the
// edge's admin API offers, beside the CLI and the console.
type APIRequest struct {
	Name   string
	Target Target
	Method string
	Path   string
	Body   []byte
}

// maxAPIBody bounds what is sent and what is printed; the admin API's own
// requests are far smaller.
const maxAPIBody = 1 << 20

var apiMethods = map[string]bool{http.MethodGet: true, http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true}

// ValidateAPIRequest admits only the admin API's own paths: never another
// host, a scheme, a dot segment or a page of the console.
func ValidateAPIRequest(r APIRequest) (APIRequest, error) {
	r.Method = strings.ToUpper(r.Method)
	if !apiMethods[r.Method] {
		return r, fmt.Errorf("method %q is not one of GET, POST, PUT, PATCH, DELETE", r.Method)
	}
	u, err := url.Parse(r.Path)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.Path, "/_/api/") {
		return r, fmt.Errorf("path %q must be an admin API path such as /_/api/zones", r.Path)
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return r, fmt.Errorf("path %q has a dot segment", r.Path)
		}
	}
	if strings.ContainsAny(r.Path, "\r\n") {
		return r, errors.New("path has a line break")
	}
	if len(r.Body) > maxAPIBody {
		return r, fmt.Errorf("body is larger than %d bytes", maxAPIBody)
	}
	if len(r.Body) > 0 && !json.Valid(r.Body) {
		return r, errors.New("body is not valid JSON")
	}
	return r, nil
}

// API sends one request to the edge's admin API through its socket, as the
// SSH user (edge.socket_group): no sudo, no token on this machine. The
// response body goes to out, indented when it is JSON. A 4xx/5xx answer is
// an error after its body is printed, so scripts can stop on it.
func API(ctx context.Context, out io.Writer, forwarder SocketForwarder, r APIRequest) error {
	e := r.Target.Edge
	if e.Socket == "" || e.SocketGroup == "" {
		return errors.New("ozalctl api needs edge.socket and edge.socket_group in the targets file (the SSH user opens the socket itself; see CLI-002)")
	}
	r, err := ValidateAPIRequest(r)
	if err != nil {
		return err
	}
	local, stop, err := forwarder.ForwardSocket(ctx, e.Socket)
	if err != nil {
		return err
	}
	defer stop()
	client := &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", local)
		}},
	}
	// ssh accepts the local connection before the edge opens its socket,
	// so only a real answer proves the SSH user may use it.
	if err := probeAPI(ctx, client); err != nil {
		return fmt.Errorf("%s: the edge did not answer on %s: %v; is the SSH user in admin.socket_group %q (usermod -aG, then log in again)?", r.Name, e.Socket, err, e.SocketGroup)
	}
	var body io.Reader
	if len(r.Body) > 0 {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, "http://localhost"+r.Path, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w (the outcome is unknown: read the state before retrying a change)", r.Method, r.Path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIBody+1))
	if err != nil {
		return err
	}
	if len(data) > maxAPIBody {
		return fmt.Errorf("the answer is larger than %d bytes", maxAPIBody)
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, data, "", "  ") == nil {
		data = append(pretty.Bytes(), '\n')
	}
	if _, err := out.Write(data); err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s: HTTP %d", r.Method, r.Path, resp.StatusCode)
	}
	return nil
}

func probeAPI(ctx context.Context, client *http.Client) error {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://localhost/_/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz answered %d", resp.StatusCode)
	}
	return nil
}
