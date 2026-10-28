package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/pipeline"
	"github.com/switchboard-project/switchboard/internal/proxy"
)

type testCA struct {
	cert  *x509.Certificate
	key   ed25519.PrivateKey
	roots *x509.CertPool
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return testCA{parsed, key, roots}
}
func (ca testCA) issue(t *testing.T, name string, usage x509.ExtKeyUsage, expired bool) tls.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if expired {
		until = time.Now().Add(-time.Minute)
	}
	cert := &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: until, DNSNames: []string{name}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

type lab struct {
	edge      *Edge
	addr      string
	clientTLS *tls.Config
	ca        testCA
	sink      *events.Recorder
}

func newLab(t *testing.T) *lab {
	t.Helper()
	ca := newTestCA(t)
	serverConfig, err := ServerTLS(ca.issue(t, "edge.localhost", x509.ExtKeyUsageServerAuth, false), ca.roots, "connector.localhost")
	if err != nil {
		t.Fatal(err)
	}
	clientConfig, err := ClientTLS(ca.issue(t, "connector.localhost", x509.ExtKeyUsageClientAuth, false), ca.roots, "edge.localhost")
	if err != nil {
		t.Fatal(err)
	}
	sink := new(events.Recorder)
	edge := NewEdge(sink)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- edge.Serve(ctx, listener, serverConfig) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(7 * time.Second):
			t.Error("edge did not stop")
		}
	})
	return &lab{edge: edge, addr: listener.Addr().String(), clientTLS: clientConfig, ca: ca, sink: sink}
}
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
func (l *lab) connect(t *testing.T, handler http.Handler) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Connect(ctx, l.addr, l.clientTLS, handler) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(7 * time.Second):
				t.Error("connector did not stop")
			}
		})
	}
	t.Cleanup(stop)
	waitFor(t, l.edge.Connected)
	return stop
}
func request(t *testing.T, client *http.Client, url, host string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}
func edgeHTTP(t *testing.T, l *lab) *httptest.Server {
	t.Helper()
	handler, closeTransport, err := l.edge.Handler("portfolio.localhost")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	server.Client().Timeout = 10 * time.Second
	t.Cleanup(func() { server.Close(); closeTransport() })
	return server
}

func TestTunnelHTTPConcurrencyTrustAndReconnect(t *testing.T) {
	l := newLab(t)
	server := edgeHTTP(t, l)
	if code, _ := request(t, server.Client(), server.URL, "portfolio.localhost"); code != 502 {
		t.Fatalf("offline code=%d", code)
	}
	if code, body := request(t, server.Client(), server.URL, "unknown.localhost"); code != 404 || strings.Contains(body, "portfolio.localhost") {
		t.Fatalf("unknown route=%d %q", code, body)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "portfolio.localhost" || r.Header.Get("X-Auth-Request-Email") != "" || r.Header.Get("X_Auth_Request_User") != "" || strings.Contains(r.Header.Get("Cookie"), "sb_session") || strings.Contains(r.Header.Get("X-Forwarded-For"), "203.0.113.99") {
			http.Error(w, "trust regression", 500)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request failed", 500)
			return
		}
		w.Header().Set("X-Lab-Upstream", "fixed")
		w.WriteHeader(201)
		_, _ = w.Write(body)
	}))
	defer upstream.Close()
	local, err := proxy.New([]config.Route{{Host: "portfolio.localhost", Target: upstream.URL}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop := l.connect(t, local)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := strings.Repeat(fmt.Sprint(i), 65536)
			req, err := http.NewRequest("POST", server.URL+"/echo?x=1", strings.NewReader(body))
			if err != nil {
				t.Error(err)
				return
			}
			req.Host = "portfolio.localhost"
			req.Header.Set("X-Auth-Request-Email", "spoofed@example.invalid")
			req.Header.Set("X_Auth_Request_User", "spoofed")
			req.Header.Set("X-Forwarded-For", "203.0.113.99")
			req.Header.Set("Cookie", "sb_session=spoofed; other=kept")
			res, err := server.Client().Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer res.Body.Close()
			got, err := io.ReadAll(res.Body)
			if err != nil || res.StatusCode != 201 || string(got) != body || res.Header.Get("X-Lab-Upstream") != "fixed" {
				t.Errorf("stream %d: status=%d bytes=%d err=%v", i, res.StatusCode, len(got), err)
			}
		}(i)
	}
	wg.Wait()
	connects := 0
	for _, event := range l.sink.Events() {
		if event.Type == TunnelConnected {
			connects++
		}
	}
	if connects != 1 {
		t.Fatalf("got %d underlying tunnel sessions, want one", connects)
	}
	stop()
	waitFor(t, func() bool { return !l.edge.Connected() })
	if code, _ := request(t, server.Client(), server.URL, "portfolio.localhost"); code != 502 {
		t.Fatalf("disconnected code=%d", code)
	}
	l.connect(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "reconnected") }))
	if code, body := request(t, server.Client(), server.URL, "portfolio.localhost"); code != 200 || body != "reconnected" {
		t.Fatalf("replacement response=%d %q", code, body)
	}
}

func TestTunnelRejectsUnauthorizedTLS(t *testing.T) {
	for _, name := range []string{"wrong-identity", "untrusted-ca", "expired", "wrong-usage", "no-certificate", "no-alpn", "wrong-server"} {
		t.Run(name, func(t *testing.T) {
			l := newLab(t)
			clientConfig := l.clientTLS.Clone()
			switch name {
			case "wrong-identity":
				clientConfig.Certificates = []tls.Certificate{l.ca.issue(t, "intruder.localhost", x509.ExtKeyUsageClientAuth, false)}
			case "untrusted-ca":
				other := newTestCA(t)
				clientConfig.Certificates = []tls.Certificate{other.issue(t, "connector.localhost", x509.ExtKeyUsageClientAuth, false)}
			case "expired":
				clientConfig.Certificates = []tls.Certificate{l.ca.issue(t, "connector.localhost", x509.ExtKeyUsageClientAuth, true)}
			case "wrong-usage":
				clientConfig.Certificates = []tls.Certificate{l.ca.issue(t, "connector.localhost", x509.ExtKeyUsageServerAuth, false)}
			case "no-certificate":
				clientConfig.Certificates = nil
			case "no-alpn":
				clientConfig.NextProtos = nil
			case "wrong-server":
				clientConfig.ServerName = "other.localhost"
			}
			dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: time.Second}, Config: clientConfig}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, err := dialer.DialContext(ctx, "tcp", l.addr)
			if err == nil {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				_, _ = conn.Write([]byte{0})
				b := make([]byte, 1)
				_, err = conn.Read(b)
			}
			if err == nil {
				t.Fatal("unauthorized TLS connection remained usable")
			}
			waitFor(t, func() bool { return l.sink.Has(TunnelRejected) })
			if l.edge.Connected() {
				t.Fatal("unauthorized connector installed")
			}
		})
	}
}

func TestDuplicateConnectorCannotReplaceActiveSession(t *testing.T) {
	l := newLab(t)
	server := edgeHTTP(t, l)
	l.connect(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "original") }))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Connect(ctx, l.addr, l.clientTLS, http.NotFoundHandler()); err == nil {
		t.Fatal("duplicate connection was not rejected")
	}
	if code, body := request(t, server.Client(), server.URL, "portfolio.localhost"); code != 200 || body != "original" {
		t.Fatalf("active connector replaced: %d %q", code, body)
	}
	found := false
	for _, event := range l.sink.Events() {
		if event.Code == "connector_busy" {
			found = true
		}
	}
	if !found {
		t.Fatal("duplicate rejection not observed")
	}
}

func TestRequestCancellationClosesOnlyItsStream(t *testing.T) {
	l := newLab(t)
	server := edgeHTTP(t, l)
	entered, exited := make(chan struct{}), make(chan struct{})
	l.connect(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			close(entered)
			<-r.Context().Done()
			close(exited)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/wait", nil)
	req.Host = "portfolio.localhost"
	done := make(chan error, 1)
	go func() {
		res, err := server.Client().Do(req)
		if res != nil {
			res.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("request never reached connector")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("request did not cancel")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request stuck")
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("connector request context not cancelled")
	}
	if code, body := request(t, server.Client(), server.URL, "portfolio.localhost"); code != 200 || body != "ok" {
		t.Fatalf("session lost: %d %q", code, body)
	}
}

func TestLabIdentitiesAndExactAuthorization(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pki")
	if err := InitLab(dir); err != nil {
		t.Fatal(err)
	}
	if err := InitLab(dir); err == nil {
		t.Fatal("existing directory overwritten")
	}
	cert, roots, err := LoadIdentity(filepath.Join(dir, "edge.crt"), filepath.Join(dir, "edge.key"), filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ServerTLS(cert, roots, "connector.localhost"); err != nil {
		t.Fatal(err)
	}
	if AuthorizeConnector([]string{"*.localhost"}, "connector.localhost") || AuthorizeConnector([]string{"connector.localhost"}, "") {
		t.Fatal("non-exact identity accepted")
	}
	if !AuthorizeConnector([]string{"connector.localhost"}, "connector.localhost") {
		t.Fatal("exact identity denied")
	}
}

// The visitor's address must survive the whole path: public side -> edge ->
// mTLS tunnel -> connector -> application. Nothing the visitor sends may
// replace it, and the internal assertion header never reaches the application.
func TestVisitorAddressCrossesTheTunnelAndCannotBeForged(t *testing.T) {
	l := newLab(t)
	var gotXFF, gotAssertion string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotAssertion = r.Header.Get(pipeline.VisitorIPHeader)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	local, err := proxy.New([]config.Route{{Host: "portfolio.localhost", Target: upstream.URL}}, nil, nil, proxy.WithAuthenticatedTunnelTrust())
	if err != nil {
		t.Fatal(err)
	}
	l.connect(t, local)
	edge, closeTransport, err := l.edge.Handler("portfolio.localhost")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTransport()

	req := httptest.NewRequest("GET", "http://portfolio.localhost/", nil)
	req.RemoteAddr = "198.51.100.7:4242"
	req.Header.Set(pipeline.VisitorIPHeader, "203.0.113.99")
	req.Header.Set("X_Switchboard_Visitor_Ip", "203.0.113.99")
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	rec := httptest.NewRecorder()
	edge.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if gotXFF != "198.51.100.7" {
		t.Errorf("application saw X-Forwarded-For %q, want 198.51.100.7", gotXFF)
	}
	if gotAssertion != "" {
		t.Errorf("the internal assertion header reached the application: %q", gotAssertion)
	}
}
