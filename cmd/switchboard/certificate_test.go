package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
)

// ARCH-010 A: the documented direct profile needs no DNS-provider token,
// trusted proxy or origin client certificate. Exercise the same autocert TLS
// configuration as production using a cached, locally signed certificate.
func TestDirectHTTPSProfileWithoutCloudflare(t *testing.T) {
	data, err := os.ReadFile("../../config.direct.example.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TLS.Enabled || cfg.TLS.DNS01 != nil || cfg.TLS.OriginClientCAFile != "" || len(cfg.TrustedProxies) != 0 || cfg.DNS.Cloudflare != nil {
		t.Fatal("direct profile acquired a proxy or DNS-provider dependency")
	}
	cfg.TLS.CacheDir = t.TempDir()
	manager := newAutocertManager(cfg, func() config.Config { return *cfg })
	ca, cached := acceptanceCertificate(t, []string{"auth.example.com", "notes.example.com"})
	for _, name := range []string{"auth.example.com", "notes.example.com"} {
		if err := manager.Cache.Put(context.Background(), name, cached); err != nil {
			t.Fatal(err)
		}
	}
	tlsConfig, handler, err := autocertTLSConfig(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), nil, manager)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = tlsConfig
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid fixture CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "notes.example.com", MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	res, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("direct TLS with trusted CA: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("direct TLS returned %d", res.StatusCode)
	}
	// A name that is neither fixed nor a route cannot start issuance.
	if err := manager.HostPolicy(context.Background(), "unknown.example.com"); err == nil {
		t.Fatal("unconfigured SNI allowed")
	}
}

type cleanupWorker struct {
	started   chan struct{}
	cancelled chan struct{}
	finish    chan struct{}
	cleaned   atomic.Bool
}

func (w *cleanupWorker) Run(ctx context.Context) {
	close(w.started)
	<-ctx.Done()
	close(w.cancelled)
	// The real issuer uses a fresh bounded context for DNS cleanup.
	<-w.finish
	w.cleaned.Store(true)
}

func TestCertificateShutdownWaitsBeforeReleasingState(t *testing.T) {
	worker := &cleanupWorker{started: make(chan struct{}), cancelled: make(chan struct{}), finish: make(chan struct{})}
	released := make(chan bool, 1)
	stop := runCertificateManager(context.Background(), worker, func() { released <- worker.cleaned.Load() })
	<-worker.started
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	<-worker.cancelled
	select {
	case <-released:
		t.Error("state released while cleanup is still active")
	default:
	}
	close(worker.finish)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join the worker")
	}
	select {
	case cleaned := <-released:
		if !cleaned {
			t.Fatal("state released before cleanup")
		}
	default:
		t.Fatal("state not released")
	}
	stop() // Idempotent: never release storage twice.
	select {
	case <-released:
		t.Fatal("state released twice")
	default:
	}
}

type unusedCertificateProvider struct{}

func (unusedCertificateProvider) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return nil, nil
}

func TestManagedTLSNeverExemptsACMEClientAuthentication(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test origin CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "origin-ca.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{TLS: config.TLS{OriginClientCAFile: path}}
	recorder := &events.Recorder{}
	tlsConfig, handler, err := managedTLSConfig(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), recorder, unusedCertificateProvider{})
	if err != nil {
		t.Fatal(err)
	}
	strict, err := tlsConfig.GetConfigForClient(&tls.ClientHelloInfo{SupportedProtos: []string{"acme-tls/1"}})
	if err != nil {
		t.Fatal(err)
	}
	if strict == nil || strict.VerifyConnection == nil {
		t.Fatal("DNS-01 retained the ACME client-auth exemption")
	}
	if err := strict.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("missing origin client certificate accepted")
	}
	req := httptest.NewRequest(http.MethodGet, "https://preview.example.com/", nil)
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("request without verified client: %d", rec.Code)
	}
}
