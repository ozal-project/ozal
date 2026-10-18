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
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
)

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
