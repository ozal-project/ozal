package originauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/events"
)

type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func newAuthority(t *testing.T, name string) authority {
	t.Helper()
	key := newKey(t)
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return authority{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

type leafOptions struct {
	usage     []x509.ExtKeyUsage
	notBefore time.Time
	notAfter  time.Time
}

func clientOptions() leafOptions {
	return leafOptions{
		usage:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		notBefore: time.Now().Add(-time.Hour),
		notAfter:  time.Now().Add(time.Hour),
	}
}

func (a authority) issue(t *testing.T, o leafOptions) tls.Certificate {
	t.Helper()
	key := newKey(t)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "cloudflare-origin-pull"},
		NotBefore:    o.notBefore,
		NotAfter:     o.notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  o.usage,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func peers(t *testing.T, c tls.Certificate) []*x509.Certificate {
	t.Helper()
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return []*x509.Certificate{leaf}
}

func poolOf(t *testing.T, a authority) *x509.CertPool {
	t.Helper()
	pool, err := LoadPool(a.pem)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestDecide(t *testing.T) {
	ours := newAuthority(t, "origin-pull-ca")
	other := newAuthority(t, "someone-elses-ca")
	roots := poolOf(t, ours)
	now := time.Now()

	expired := clientOptions()
	expired.notBefore, expired.notAfter = now.Add(-2*time.Hour), now.Add(-time.Hour)
	serverOnly := clientOptions()
	serverOnly.usage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}

	for _, tc := range []struct {
		name  string
		peers []*x509.Certificate
		want  Code
	}{
		{"no certificate", nil, NoCertificate},
		{"issued by our CA", peers(t, ours.issue(t, clientOptions())), Allowed},
		{"issued by another CA", peers(t, other.issue(t, clientOptions())), Untrusted},
		{"expired", peers(t, ours.issue(t, expired)), Expired},
		{"server-only key usage", peers(t, ours.issue(t, serverOnly)), Invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.peers, roots, now)
			if got.Code != tc.want || got.Allowed != (tc.want == Allowed) {
				t.Fatalf("Decide = %+v, want code %s", got, tc.want)
			}
		})
	}

	t.Run("a pinned self-signed certificate is its own root", func(t *testing.T) {
		pinned := newAuthority(t, "pinned")
		got := Decide([]*x509.Certificate{pinned.cert}, poolOf(t, pinned), now)
		// A CA certificate without client-auth usage restrictions is accepted
		// only because it is the configured root itself.
		if got.Code != Allowed {
			t.Fatalf("Decide = %+v, want allowed", got)
		}
	})
}

func TestDenialsAreAggregatedPerWindow(t *testing.T) {
	recorder := &events.Recorder{}
	emitter := newDenialEmitter(recorder)
	now := time.Unix(1_700_000_000, 0)
	emitter.now = func() time.Time { return now }

	emitter.emit(Untrusted, "reason", "192.0.2.1")
	for i := 0; i < 25; i++ {
		emitter.emit(Untrusted, "reason", "192.0.2.1")
	}
	if got := len(recorder.Events()); got != 1 {
		t.Fatalf("events inside one window = %d, want 1", got)
	}
	if first := recorder.Events()[0]; first.Count != 0 || first.Code != string(Untrusted) {
		t.Fatalf("first event = %+v, want count 0 and the denial code", first)
	}

	now = now.Add(2 * time.Minute)
	emitter.emit(Untrusted, "reason", "192.0.2.1")
	list := recorder.Events()
	if len(list) != 2 {
		t.Fatalf("events after the window = %d, want 2", len(list))
	}
	if list[1].Count != 25 {
		t.Fatalf("second event count = %d, want 25 suppressed denials", list[1].Count)
	}
}

func TestLoadPoolRejectsUnusableFiles(t *testing.T) {
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")})
	for name, data := range map[string][]byte{"empty": nil, "garbage": []byte("not pem"), "private key": key} {
		if _, err := LoadPool(data); err == nil {
			t.Errorf("%s was accepted as a trust bundle", name)
		}
	}
}

func serverCert(t *testing.T) tls.Certificate {
	t.Helper()
	ca := newAuthority(t, "server-ca")
	return ca.issue(t, leafOptions{
		usage:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		notBefore: time.Now().Add(-time.Hour),
		notAfter:  time.Now().Add(time.Hour),
	})
}

// startServer runs a TLS listener configured by ServerTLS and answers "ok" to
// every connection whose handshake the policy lets through. It opts into the
// ACME validation exemption, like the autocert HTTP-01/TLS-ALPN listener.
func startServer(t *testing.T, roots *x509.CertPool, sink events.Sink) string {
	t.Helper()
	base := &tls.Config{Certificates: []tls.Certificate{serverCert(t)}}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", ServerTLS(base, roots, sink, AllowACMEValidation()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if err := conn.(*tls.Conn).Handshake(); err != nil {
					return
				}
				_, _ = conn.Write([]byte("ok"))
			}()
		}
	}()
	return listener.Addr().String()
}

// pull connects and reports whether the server answered. In TLS 1.3 the client
// finishes its handshake before the server checks the client certificate, so a
// rejection only shows up when the client reads.
func pull(t *testing.T, addr string, client *tls.Certificate) error {
	t.Helper()
	cfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the test cares about the client side only
	if client != nil {
		cfg.Certificates = []tls.Certificate{*client}
	}
	conn, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2)
	_, err = conn.Read(buf)
	return err
}

func TestServerTLSRequiresOurClientCertificate(t *testing.T) {
	ours := newAuthority(t, "origin-pull-ca")
	other := newAuthority(t, "someone-elses-ca")
	rec := &events.Recorder{}
	addr := startServer(t, poolOf(t, ours), rec)

	good := ours.issue(t, clientOptions())
	if err := pull(t, addr, &good); err != nil {
		t.Fatalf("the approved client was refused: %v", err)
	}
	if rec.Has(OriginDenied) {
		t.Fatal("an approved handshake emitted a denial")
	}

	foreign := other.issue(t, clientOptions())
	expired := clientOptions()
	expired.notBefore, expired.notAfter = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
	old := ours.issue(t, expired)
	for name, client := range map[string]*tls.Certificate{"no certificate": nil, "other CA": &foreign, "expired": &old} {
		if err := pull(t, addr, client); err == nil {
			t.Errorf("%s: the server answered a client it must refuse", name)
		}
	}

	denied, ok := rec.Find(OriginDenied)
	if !ok {
		t.Fatal("refused handshakes emitted no event")
	}
	if denied.Code == "" || denied.RemoteAddr == "" {
		t.Fatalf("denial lacks a code or remote address: %+v", denied)
	}
}

// ACME tls-alpn-01 validation connects without a client certificate: only the
// exact exchange may skip the client-certificate policy, and only on a
// listener that opted into the exemption.
func TestServerTLSLetsACMEChallengesThrough(t *testing.T) {
	roots := poolOf(t, newAuthority(t, "origin-pull-ca"))
	cfg := ServerTLS(&tls.Config{}, roots, events.Discard, AllowACMEValidation())

	acme, err := cfg.GetConfigForClient(&tls.ClientHelloInfo{SupportedProtos: []string{"acme-tls/1"}})
	if err != nil || acme != nil {
		t.Fatalf("exact ACME hello: config=%v err=%v, want the unmodified base config (nil, nil)", acme, err)
	}

	for _, protos := range [][]string{{"h2", "http/1.1"}, {"acme-tls/1", "http/1.1"}, nil} {
		strict, err := cfg.GetConfigForClient(&tls.ClientHelloInfo{SupportedProtos: protos})
		if err != nil || strict == nil || strict.ClientAuth == tls.NoClientCert || strict.VerifyConnection == nil {
			t.Errorf("hello %v: the client-certificate policy was skipped", protos)
		}
		if strict != nil && strict.MinVersion < tls.VersionTLS12 {
			t.Errorf("hello %v: MinVersion = %x, want at least TLS 1.2", protos, strict.MinVersion)
		}
	}
}

// Regression for DNS-01 mode: a listener that never answers an ACME
// tls-alpn-01 validation must not exempt it. Without the fix the exact ACME
// hello skipped the client-certificate policy and reached net/http.
func TestServerTLSWithoutOptInRefusesACMEHello(t *testing.T) {
	roots := poolOf(t, newAuthority(t, "origin-pull-ca"))
	cfg := ServerTLS(&tls.Config{}, roots, events.Discard)

	strict, err := cfg.GetConfigForClient(&tls.ClientHelloInfo{SupportedProtos: []string{"acme-tls/1"}})
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if strict == nil || strict.ClientAuth != tls.RequestClientCert || strict.VerifyConnection == nil {
		t.Fatal("an acme-tls/1 hello must be held to the client-certificate policy by default")
	}
	d := Decide(nil, roots, time.Now())
	if d.Allowed {
		t.Fatal("a connection without a client certificate must be refused")
	}
}

// A real connection offering acme-tls/1 against a listener without the
// opt-in must fail the handshake instead of reaching the handler. The server
// answers "ok" only when its handshake policy accepted the connection.
func TestServerTLSWithoutOptInRejectsRealACMEConnection(t *testing.T) {
	roots := poolOf(t, newAuthority(t, "origin-pull-ca"))
	base := &tls.Config{Certificates: []tls.Certificate{serverCert(t)}, NextProtos: []string{"acme-tls/1"}}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", ServerTLS(base, roots, events.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if err := conn.(*tls.Conn).Handshake(); err != nil {
					return
				}
				_, _ = conn.Write([]byte("ok"))
			}()
		}
	}()

	client, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // the test only observes the handshake outcome
		NextProtos:         []string{"acme-tls/1"},
	})
	if err != nil {
		return // the handshake failed outright: the door is closed
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2)
	if _, err := client.Read(buf); err == nil {
		t.Fatal("the acme-tls/1 connection without a client certificate was answered")
	}
}

// A connection that negotiated acme-tls/1 reaches net/http, which would serve
// it as plain HTTP: no request may get through without a verified certificate.
func TestRequireVerifiedFailsClosed(t *testing.T) {
	reached := false
	handler := RequireVerified(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))

	ours := newAuthority(t, "origin-pull-ca")
	good := peers(t, ours.issue(t, clientOptions()))

	for name, state := range map[string]*tls.ConnectionState{
		"plain HTTP":        nil,
		"no peer cert":      {},
		"acme-tls/1 bypass": {NegotiatedProtocol: "acme-tls/1"},
	} {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "https://origin.test/", nil)
		req.TLS = state
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if reached || rec.Code != http.StatusForbidden {
			t.Errorf("%s: reached=%v status=%d, want refused with 403", name, reached, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "https://origin.test/", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: good}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !reached {
		t.Error("a request with a verified client certificate was refused")
	}
}

// The whole path through a real http.Server: the policy must not cost HTTP/2,
// and a request without an approved certificate must never reach the handler.
func TestHTTPServerServesOnlyApprovedClients(t *testing.T) {
	ours := newAuthority(t, "origin-pull-ca")
	rec := &events.Recorder{}
	base := &tls.Config{Certificates: []tls.Certificate{serverCert(t)}, NextProtos: []string{"h2", "http/1.1", "acme-tls/1"}}
	srv := &http.Server{
		TLSConfig: ServerTLS(base, poolOf(t, ours), rec),
		Handler: RequireVerified(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "hello")
		})),
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })

	client := func(cert *tls.Certificate) *http.Client {
		cfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the test cares about the client certificate only
		if cert != nil {
			cfg.Certificates = []tls.Certificate{*cert}
		}
		return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: true}}
	}
	url := "https://" + listener.Addr().String() + "/"

	good := ours.issue(t, clientOptions())
	resp, err := client(&good).Get(url)
	if err != nil {
		t.Fatalf("approved client: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "hello" {
		t.Fatalf("approved client got %d %q", resp.StatusCode, body)
	}
	if resp.ProtoMajor != 2 {
		t.Errorf("negotiated HTTP/%d, want HTTP/2: the policy must not cost h2", resp.ProtoMajor)
	}

	if resp, err := client(nil).Get(url); err == nil {
		resp.Body.Close()
		t.Fatal("a client without a certificate got a response")
	}
	if !rec.Has(OriginDenied) {
		t.Fatal("the refused client left no event")
	}
}
