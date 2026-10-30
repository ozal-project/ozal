package ctl

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateAPIRequestAdmitsOnlyAdminAPIPaths(t *testing.T) {
	for _, c := range []struct {
		method, path, body string
		ok                 bool
	}{
		{"get", "/_/api/zones", "", true},
		{"POST", "/_/api/zones", `{"name":"example.org"}`, true},
		{"DELETE", "/_/api/zones/example.org?x=1", `{}`, true},
		{"GET", "http://evil.example/_/api/zones", "", false},
		{"GET", "//evil.example/_/api/zones", "", false},
		{"GET", "/_/status", "", false},
		{"GET", "/_/api/../status", "", false},
		{"GET", "/_/api/./zones", "", false},
		{"GET", "_/api/zones", "", false},
		{"TRACE", "/_/api/zones", "", false},
		{"POST", "/_/api/zones", `{not json`, false},
		{"POST", "/_/api/zones", strings.Repeat(" ", maxAPIBody+1), false},
	} {
		_, err := ValidateAPIRequest(APIRequest{Method: c.method, Path: c.path, Body: []byte(c.body)})
		if (err == nil) != c.ok {
			t.Errorf("%s %s: err = %v, want ok=%t", c.method, c.path, err, c.ok)
		}
	}
}

// socketForwarder serves handler on a private unix socket, standing in for
// the SSH-forwarded admin socket.
type socketForwarder struct {
	t       *testing.T
	handler http.Handler
	remote  string
	stopped bool
}

func (f *socketForwarder) ForwardSocket(_ context.Context, remote string) (string, func(), error) {
	f.remote = remote
	dir, err := os.MkdirTemp("", "ctl")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "a.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		return "", nil, err
	}
	server := &http.Server{Handler: f.handler}
	go server.Serve(listener)
	return path, func() { f.stopped = true; server.Close(); os.RemoveAll(dir) }, nil
}

func apiTarget() Target {
	target := goodTarget()
	target.Edge.Socket = "/run/ozal/admin.sock"
	target.Edge.SocketGroup = "ozal-admin"
	return target
}

func skipWithoutUnixSockets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets")
	}
}

func TestAPISendsTheRequestThroughTheEdgeSocket(t *testing.T) {
	skipWithoutUnixSockets(t)
	var method, path, body, contentType string
	forwarder := &socketForwarder{t: t, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_/healthz" {
			return
		}
		data, _ := io.ReadAll(r.Body)
		method, path, body, contentType = r.Method, r.URL.RequestURI(), string(data), r.Header.Get("Content-Type")
		w.Write([]byte(`{"status":"zone.added","config_sha256":"d2"}`))
	})}
	var out bytes.Buffer
	err := API(context.Background(), &out, forwarder, APIRequest{Name: "edge", Target: apiTarget(),
		Method: "post", Path: "/_/api/zones", Body: []byte(`{"name":"example.org","expected":"d1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if forwarder.remote != "/run/ozal/admin.sock" || !forwarder.stopped {
		t.Fatalf("forwarded %q, stopped=%t", forwarder.remote, forwarder.stopped)
	}
	if method != "POST" || path != "/_/api/zones" || body != `{"name":"example.org","expected":"d1"}` || contentType != "application/json" {
		t.Fatalf("edge saw %s %s %q (%s)", method, path, body, contentType)
	}
	if !strings.Contains(out.String(), "\n  \"status\": \"zone.added\"") {
		t.Fatalf("answer not printed as indented JSON: %q", out.String())
	}
}

// An HTTP error prints the edge's explanation and fails, so a script stops.
func TestAPIErrorStatusFailsAfterPrintingTheAnswer(t *testing.T) {
	skipWithoutUnixSockets(t)
	forwarder := &socketForwarder{t: t, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_/healthz" {
			return
		}
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"configuration changed; refresh and review again"}`))
	})}
	var out bytes.Buffer
	err := API(context.Background(), &out, forwarder, APIRequest{Target: apiTarget(), Method: "POST", Path: "/_/api/zones", Body: []byte(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "409") || !strings.Contains(out.String(), "refresh and review") {
		t.Fatalf("err = %v, out = %q", err, out.String())
	}
}

// ssh accepts locally even when the edge refuses the SSH user: without a
// real answer the command stops before sending anything.
func TestAPIStopsWhenTheEdgeDoesNotAnswer(t *testing.T) {
	skipWithoutUnixSockets(t)
	sent := false
	forwarder := &socketForwarder{t: t, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_/healthz" {
			sent = true
		}
		hijacker, _ := w.(http.Hijacker)
		conn, _, _ := hijacker.Hijack()
		conn.Close() // what a refused stream-local channel looks like
	})}
	err := API(context.Background(), io.Discard, forwarder, APIRequest{Target: apiTarget(), Method: "POST", Path: "/_/api/zones", Body: []byte(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "usermod") || sent {
		t.Fatalf("err = %v, request sent = %t", err, sent)
	}
}

func TestAPINeedsTheSocketGroup(t *testing.T) {
	target := goodTarget()
	target.Edge.Socket = "/run/ozal/admin.sock"
	forwarder := &socketForwarder{t: t}
	if err := API(context.Background(), io.Discard, forwarder, APIRequest{Target: target, Method: "GET", Path: "/_/api/zones"}); err == nil || !strings.Contains(err.Error(), "socket_group") {
		t.Fatalf("err = %v", err)
	}
	if forwarder.remote != "" {
		t.Fatal("a tunnel was opened without socket_group")
	}
}
