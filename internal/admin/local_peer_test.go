//go:build linux

package admin

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
)

// The socket's peer comes from the kernel: a request over a real unix
// socket from the service's own uid (the sudo -u path) stays "local".
func TestLocalConnRecordsThePeerUID(t *testing.T) {
	dir, err := os.MkdirTemp("", "sb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "a.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	type seen struct {
		actor string
		uid   int
		ok    bool
	}
	got := make(chan seen, 1)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid, ok := r.Context().Value(peerKey{}).(int)
			got <- seen{actorOf(r), uid, ok}
		}),
		ConnContext: LocalConn,
	}
	go server.Serve(listener)
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
	resp, err := client.Get("http://localhost/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	s := <-got
	if !s.ok || s.uid != os.Getuid() {
		t.Fatalf("peer uid = %d (known=%t), want %d", s.uid, s.ok, os.Getuid())
	}
	if s.actor != "local" {
		t.Fatalf("actor = %q, want local for the service's own uid", s.actor)
	}
}

func actorOf(r *http.Request) string { return actor(r) }

// Another account on the shared socket is named; a header can never do it.
func TestLocalActorNamesAnotherAccount(t *testing.T) {
	other := os.Getuid() + 1
	if os.Getuid() != 0 {
		other = 0
	}
	ctx := context.WithValue(context.WithValue(context.Background(), localKey{}, true), peerKey{}, other)
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	r.Header.Set("X-Ozal-Actor", "someone-else")
	want := "local:uid=" + strconv.Itoa(other)
	if account, err := user.LookupId(strconv.Itoa(other)); err == nil {
		want = "local:" + account.Username
	}
	if got := actor(r); got != want {
		t.Fatalf("actor = %q, want %q", got, want)
	}
	// TCP requests are never local, whatever the context of another request.
	tcp, _ := http.NewRequest(http.MethodGet, "/", nil)
	tcp.RemoteAddr = "127.0.0.1:5555"
	if got := actor(tcp); got != "127.0.0.1:5555" {
		t.Fatalf("tcp actor = %q", got)
	}
}
