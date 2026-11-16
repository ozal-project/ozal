package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCodeEdge plays the admin socket for a code enrolment, walking through
// the states the page would cause.
type fakeCodeEdge struct {
	mu        sync.Mutex
	loginHost bool
	states    []string
	operator  string
	from      string
	cancelled bool
	linkAsked bool
}

func (f *fakeCodeEdge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	const watch = "/_/api/admin/enrolments/code/poll-secret"
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/_/api/admin/enrolments":
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["mode"] != "code" {
			f.linkAsked = true
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"token":"link-token"}`))
			return
		}
		if !f.loginHost {
			w.WriteHeader(http.StatusPreconditionFailed)
			w.Write([]byte(`{"error":"enrolment codes need admin.login_host","code":"login_host_off"}`))
			return
		}
		f.operator, f.from = req["operator"], req["from"]
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"poll": "poll-secret", "code": "ABCD-EFGH", "url": "https://auth.example.com/_/auth/admin/code", "claim_by": time.Now().Add(5 * time.Minute)})
	case r.Method == http.MethodGet && r.URL.Path == watch:
		state := f.states[0]
		if len(f.states) > 1 {
			f.states = f.states[1:]
		}
		json.NewEncoder(w).Encode(map[string]any{"state": state, "failures": 2, "country": "PY", "browser": "Chrome on macOS", "address": "203.0.113.7", "claimed": time.Now(), "account": "Ana <ana@example.com>"})
	case r.Method == http.MethodDelete && r.URL.Path == watch:
		f.cancelled = true
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}
}

func serveFake(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ozal-code")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "admin.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return socket
}

func enrolWith(t *testing.T, ctx context.Context, socket string, extra ...string) (string, error) {
	t.Helper()
	t.Setenv("SSH_CONNECTION", "192.168.100.5 50000 192.168.100.12 22")
	t.Setenv("SUDO_USER", "saint")
	var out bytes.Buffer
	args := append([]string{"enrol"}, extra...)
	args = append(args, "-admin", "unix:"+socket, "-url", "http://127.0.0.1:9090")
	err := runConsole(ctx, args, &out)
	return out.String(), err
}

// The terminal only shows the code and reports what happened on the page:
// which browser used it and who was enrolled. It asks nothing.
func TestEnrolByCodeShowsTheCodeAndReportsTheOutcome(t *testing.T) {
	edge := &fakeCodeEdge{loginHost: true, states: []string{"claimed", "claimed", "done"}}
	out, err := enrolWith(t, context.Background(), serveFake(t, edge))
	if err != nil {
		t.Fatalf("enrol: %v\n%s", err, out)
	}
	for _, want := range []string{
		"https://auth.example.com/_/auth/admin/code", "ABCD-EFGH",
		"Code entered in Chrome on macOS from 203.0.113.7 (PY)",
		"Passkey created for Ana <ana@example.com>", "http://ozal.localhost:9090/overview",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "Code entered") != 1 {
		t.Errorf("the claim was reported more than once:\n%s", out)
	}
	if strings.Contains(out, "[y/N]") {
		t.Error("the terminal asked a question")
	}
	if edge.operator != "saint" || edge.from != "192.168.100.5" || edge.cancelled {
		t.Fatalf("operator %q, from %q, cancelled %v", edge.operator, edge.from, edge.cancelled)
	}
	if strings.Count(out, "wrong code") != 1 {
		t.Errorf("wrong codes were not reported exactly once:\n%s", out)
	}
}

// "This wasn't me" on the page ends the command with nothing created.
func TestEnrolByCodeReportsAWithdrawal(t *testing.T) {
	edge := &fakeCodeEdge{loginHost: true, states: []string{"withdrawn"}}
	out, err := enrolWith(t, context.Background(), serveFake(t, edge))
	if err == nil || !strings.Contains(err.Error(), "cancelled on the page") {
		t.Fatalf("err = %v\n%s", err, out)
	}
}

// Without admin.login_host the command prints the console link as before.
func TestEnrolFallsBackToTheLinkWithoutALoginHost(t *testing.T) {
	edge := &fakeCodeEdge{}
	out, err := enrolWith(t, context.Background(), serveFake(t, edge))
	if err != nil {
		t.Fatal(err)
	}
	if !edge.linkAsked || !strings.Contains(out, "printing a console link instead") || !strings.Contains(out, "/overview#enrol=link-token") {
		t.Fatalf("output:\n%s", out)
	}
}

// -link keeps the link even when codes are available.
func TestEnrolLinkFlagSkipsTheCode(t *testing.T) {
	edge := &fakeCodeEdge{loginHost: true, states: []string{"pending"}}
	out, err := enrolWith(t, context.Background(), serveFake(t, edge), "-link")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "ABCD-EFGH") || !strings.Contains(out, "#enrol=link-token") {
		t.Fatalf("output:\n%s", out)
	}
}

// An interrupted command withdraws its code, so it cannot be used later.
func TestEnrolByCodeWithdrawsTheCodeWhenInterrupted(t *testing.T) {
	edge := &fakeCodeEdge{loginHost: true, states: []string{"pending"}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := enrolWith(t, ctx, serveFake(t, edge)); err == nil {
		t.Fatal("an interrupted enrolment reported success")
	}
	edge.mu.Lock()
	defer edge.mu.Unlock()
	if !edge.cancelled {
		t.Fatal("the code stayed usable after the interruption")
	}
}

// An edge from before codes refuses the new fields with 400; the command
// falls back to the link instead of failing.
func TestEnrolFallsBackToTheLinkOnAnOlderEdge(t *testing.T) {
	older := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if _, ok := req["mode"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"kind must be bootstrap or recover"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"token":"link-token"}`))
	})
	out, err := enrolWith(t, context.Background(), serveFake(t, older))
	if err != nil || !strings.Contains(out, "#enrol=link-token") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}
}

// A replaced code ends the command with a message that names the reason.
func TestEnrolByCodeReportsAReplacement(t *testing.T) {
	edge := &fakeCodeEdge{loginHost: true, states: []string{"replaced"}}
	if _, err := enrolWith(t, context.Background(), serveFake(t, edge)); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("err = %v", err)
	}
}
