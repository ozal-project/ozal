package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/events"
)

// enrolledLab is an edge in registry mode whose Serve can be stopped and
// restarted on the same address, to exercise reconnection.
type enrolledLab struct {
	edgeTLS *tls.Config
	ca      testCA
	reg     *Registry
	addr    string
	sink    *events.Recorder

	mu     sync.Mutex
	edge   *Edge
	cancel context.CancelFunc
	done   chan error
}

func newEnrolledLab(t *testing.T) *enrolledLab {
	t.Helper()
	ca := newTestCA(t)
	edgeTLS, err := ServerTLSRegistry(ca.issue(t, "edge.localhost", x509.ExtKeyUsageServerAuth, false), ca.roots)
	if err != nil {
		t.Fatal(err)
	}
	l := &enrolledLab{edgeTLS: edgeTLS, ca: ca, reg: newTestRegistry(t), sink: new(events.Recorder)}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.addr = probe.Addr().String()
	probe.Close()
	l.startEdge(t)
	t.Cleanup(func() { l.stopEdge(t) })
	return l
}

func (l *enrolledLab) startEdge(t *testing.T) {
	t.Helper()
	edge := NewEdge(l.sink)
	edge.UseAuthorizer(l.reg)
	edge.revalidate = 20 * time.Millisecond
	var listener net.Listener
	var err error
	for i := 0; i < 100; i++ { // the old listener may still be closing
		if listener, err = net.Listen("tcp", l.addr); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- edge.Serve(ctx, listener, l.edgeTLS) }()
	l.mu.Lock()
	l.edge, l.cancel, l.done = edge, cancel, done
	l.mu.Unlock()
}

func (l *enrolledLab) stopEdge(t *testing.T) {
	t.Helper()
	l.mu.Lock()
	cancel, done := l.cancel, l.done
	l.cancel = nil
	l.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(7 * time.Second):
		t.Error("edge did not stop")
	}
}

func (l *enrolledLab) current() *Edge {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.edge
}

// enroll issues a connector certificate, registers it under name and returns
// the client TLS config.
func (l *enrolledLab) enroll(t *testing.T, name string) *tls.Config {
	t.Helper()
	cert := l.ca.issue(t, name+".localhost", x509.ExtKeyUsageClientAuth, false)
	if err := l.reg.Enroll(context.Background(), name, FingerprintOf(cert.Certificate[0])); err != nil {
		t.Fatal(err)
	}
	cfg, err := ClientTLS(cert, l.ca.roots, "edge.localhost")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func okHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
}

func runLoop(t *testing.T, l *enrolledLab, cfg *tls.Config, link *Link, body string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	opts := LoopOptions{Backoff: Backoff{Base: 10 * time.Millisecond, Max: 50 * time.Millisecond}, Link: link}
	go func() { done <- ConnectLoop(ctx, l.addr, cfg, okHandler(body), opts) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(7 * time.Second):
			t.Error("connect loop did not stop")
		}
	})
}

func hasConnector(l *enrolledLab, name string) func() bool {
	return func() bool {
		for _, s := range l.current().Status() {
			if s.Name == name {
				return true
			}
		}
		return false
	}
}

func TestEnrolledConnectorsAttachByNameAndRouteSeparately(t *testing.T) {
	l := newEnrolledLab(t)
	runLoop(t, l, l.enroll(t, "alpha"), nil, "from-alpha")
	runLoop(t, l, l.enroll(t, "beta"), nil, "from-beta")
	waitFor(t, hasConnector(l, "alpha"))
	waitFor(t, hasConnector(l, "beta"))

	for _, c := range []struct{ host, connector, want string }{
		{"a.localhost", "alpha", "from-alpha"},
		{"b.localhost", "beta", "from-beta"},
	} {
		handler, closeTransport, err := l.current().HandlerFor(c.host, c.connector)
		if err != nil {
			t.Fatal(err)
		}
		srv := newHTTPServer(t, handler, closeTransport)
		if code, body := request(t, srv.Client(), srv.URL, c.host); code != 200 || body != c.want {
			t.Fatalf("%s via %s: %d %q, want %q", c.host, c.connector, code, body, c.want)
		}
	}
}

func TestUnenrolledCertificateFromOurCAIsRejected(t *testing.T) {
	l := newEnrolledLab(t)
	stranger := l.ca.issue(t, "stranger.localhost", x509.ExtKeyUsageClientAuth, false)
	cfg, err := ClientTLS(stranger, l.ca.roots, "edge.localhost")
	if err != nil {
		t.Fatal(err)
	}
	link := new(Link)
	runLoop(t, l, cfg, link, "never")
	waitFor(t, func() bool { return l.sink.Has(TunnelRejected) })
	if e, _ := l.sink.Find(TunnelRejected); e.Code != "connector_unauthorized" {
		t.Fatalf("rejection code = %q", e.Code)
	}
	if len(l.current().Status()) != 0 {
		t.Fatal("unenrolled connector attached")
	}
}

func TestRevocationClosesLiveSessionAndBlocksReconnect(t *testing.T) {
	l := newEnrolledLab(t)
	runLoop(t, l, l.enroll(t, "alpha"), nil, "x")
	waitFor(t, hasConnector(l, "alpha"))

	if err := l.reg.Revoke(context.Background(), "alpha"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !hasConnector(l, "alpha")() })
	// The loop keeps retrying but must never attach again.
	time.Sleep(300 * time.Millisecond)
	if hasConnector(l, "alpha")() {
		t.Fatal("revoked connector re-attached")
	}
}

func TestConnectorReattachesAfterEdgeRestart(t *testing.T) {
	l := newEnrolledLab(t)
	link := new(Link)
	runLoop(t, l, l.enroll(t, "alpha"), link, "x")
	waitFor(t, hasConnector(l, "alpha"))
	waitFor(t, func() bool { return link.Status().State == LinkConnected })

	l.stopEdge(t)
	waitFor(t, func() bool { return link.Status().State == LinkBackoff })
	if s := link.Status(); s.Attempt == 0 || s.LastError == "" || s.NextRetry.IsZero() {
		t.Fatalf("backoff status incomplete: %+v", s)
	}

	l.startEdge(t)
	waitFor(t, hasConnector(l, "alpha"))
	waitFor(t, func() bool { return link.Status().State == LinkConnected })
}

func TestConnectLoopRejectsUnusableTLSImmediately(t *testing.T) {
	err := ConnectLoop(context.Background(), "127.0.0.1:1", &tls.Config{InsecureSkipVerify: true}, okHandler("x"), LoopOptions{})
	if err == nil {
		t.Fatal("insecure TLS config accepted")
	}
}

func newHTTPServer(t *testing.T, h http.Handler, closeTransport func()) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	srv.Client().Timeout = 10 * time.Second
	t.Cleanup(func() { srv.Close(); closeTransport() })
	return srv
}
