package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/passkey"
	"github.com/ozal-project/ozal/internal/routemanager"
	"github.com/ozal-project/ozal/internal/store/sqlite"
)

// TestHTTPSAcceptanceFixture is an opt-in, disposable browser fixture, not a
// production listener mode. The Node driver starts this test executable and
// closes stdin to stop it. Everything binds loopback with OS-assigned ports.
func TestHTTPSAcceptanceFixture(t *testing.T) {
	metadata := os.Getenv("OZAL_HTTPS_FIXTURE")
	if metadata == "" {
		t.Skip("started by scripts/demo/https-host-sessions-acceptance.mjs")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	host := func(name string) string { return fmt.Sprintf("%s:%d", name, port) }
	login := "https://" + host("auth.login.test")
	names := []string{"auth.login.test", "app.login.test", "notes.other.test", "wiki.third.test", "denied.fourth.test"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		cookies := "clean"
		if strings.Contains(r.Header.Get("Cookie"), "sb_session") || strings.Contains(r.Header.Get("Cookie"), "sb_handoff") {
			cookies = "leaked"
		}
		fmt.Fprintf(w, "<!doctype html><title>Private app</title><h1>Private app</h1><p id=cookies>%s</p>", cookies)
	}))
	defer upstream.Close()
	cfg := config.Config{
		Listen: listener.Addr().String(),
		Admin:  config.Admin{Listen: listener.Addr().String()},
		TLS:    config.TLS{Enabled: true, Domains: names, CacheDir: filepath.Join(dir, "acme")},
		Auth:   config.Auth{CookieDomain: "login.test", Passkeys: &config.Passkeys{RPID: "login.test", LoginURL: login, DisplayName: "HTTPS acceptance"}},
	}
	for _, name := range names[1:] {
		policy := &config.AccessPolicy{AllowedDomains: []string{"acme.dev"}}
		if name == "denied.fourth.test" {
			policy = &config.AccessPolicy{AllowedEmails: []string{"nobody@acme.dev"}}
		}
		cfg.Routes = append(cfg.Routes, config.Route{Host: host(name), Target: upstream.URL, Access: policy})
	}
	configPath := filepath.Join(dir, "fixture.json")
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); db.Close() }()
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	identity := sqlite.NewIdentity(db)
	manager, err := routemanager.New(ctx, configPath, time.Hour,
		routemanager.WithPasskeyDirectory(identity),
		routemanager.WithSessionBackend(func(lifetime auth.Lifetime) auth.SessionBackend { return sqlite.NewSessions(db, lifetime) }))
	if err != nil {
		t.Fatal(err)
	}
	autocert := newAutocertManager(&cfg, manager.Config)
	ca, cached := acceptanceCertificate(t, names)
	for _, name := range names {
		if err := autocert.Cache.Put(ctx, name, cached); err != nil {
			t.Fatal(err)
		}
	}
	caPath := filepath.Join(dir, "public-ca.crt")
	if err := os.WriteFile(caPath, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	tlsConfig, handler, err := autocertTLSConfig(&cfg, manager, nil, autocert)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, TLSConfig: tlsConfig, ReadHeaderTimeout: readHeaderTimeout, IdleTimeout: idleTimeout}
	done := make(chan error, 1)
	go func() { done <- server.ServeTLS(listener, "", "") }()
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; err != http.ErrServerClosed {
			t.Error(err)
		}
	}()
	// Test control is separate from the real edge and never compiled into the
	// product. Expiry changes fixture rows, never production lifetime rules.
	token, err := auth.NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var input struct{ Email string }
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input); err != nil {
			http.Error(w, "bad fixture request", 400)
			return
		}
		var err error
		switch r.URL.Path {
		case "/invite":
			var invitation sqlite.Invitation
			invitation, err = identity.Invite(r.Context(), input.Email, "Acceptance", nil, false, 15*time.Minute)
			if err == nil {
				var link string
				link, err = passkey.InviteLink(cfg, invitation.Token, "")
				if err == nil {
					json.NewEncoder(w).Encode(map[string]string{"link": link})
					return
				}
			}
		case "/disable":
			err = identity.SetDisabled(r.Context(), input.Email, true)
		case "/expire":
			_, err = db.ExecContext(r.Context(), `UPDATE sessions SET expires_at = ? WHERE email = ? AND parent_hash IS NULL`, time.Now().Add(-time.Minute).Unix(), input.Email)
		case "/revoke":
			_, err = db.ExecContext(r.Context(), `DELETE FROM sessions WHERE email = ? AND parent_hash IS NULL`, input.Email)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "fixture operation failed", 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer control.Close()
	encoded, err = json.Marshal(map[string]any{"login": login, "port": port, "ca": caPath, "control": control.URL, "token": token})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadata, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(10 * time.Minute):
		t.Fatal("browser driver did not stop fixture")
	}
}

// Only ephemeral generated keys enter the autocert fixture cache. Certificates
// have realistic validity so autocert never attempts renewal against a public CA.
func acceptanceCertificate(t *testing.T, names []string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Disposable Ozal test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(0, 3, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
	cached := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateDER})
	cached = append(cached, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})...)
	cached = append(cached, ca...)
	return ca, cached
}
