package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCert(t *testing.T, dir string) string {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "c"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "c.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConnectorAtomicRotationCommand(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "state.db")
	if err := runMigrate(ctx, []string{"-db", db}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	old, next := writeCert(t, t.TempDir()), writeCert(t, t.TempDir())
	oldFP, _ := fingerprintFromPEM(old)
	nextFP, _ := fingerprintFromPEM(next)
	expected := hex.EncodeToString(oldFP[:])
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runConnector(ctx, args, &out)
		return strings.TrimSpace(out.String()), err
	}
	if _, err := run("enroll", "-db", db, "-name", "portfolio", "-cert", old); err != nil {
		t.Fatal(err)
	}
	if got, err := run("current", "-db", db, "-name", "portfolio"); err != nil || got != expected {
		t.Fatalf("current: %s %v", got, err)
	}
	if _, err := run("rotate", "-db", db, "-name", "portfolio", "-cert", next, "-expected", "bad"); err == nil {
		t.Fatal("malformed fingerprint accepted")
	}
	if _, err := run("rotate", "-db", db, "-name", "portfolio", "-cert", next, "-expected", expected); err != nil {
		t.Fatal(err)
	}
	if got, err := run("current", "-db", db, "-name", "portfolio"); err != nil || got != hex.EncodeToString(nextFP[:]) {
		t.Fatalf("rotated current: %s %v", got, err)
	}
	if _, err := run("rotate", "-db", db, "-name", "portfolio", "-cert", old, "-expected", expected); err == nil {
		t.Fatal("stale rotation accepted")
	}
	if _, err := run("revoke", "-db", db, "-name", "portfolio"); err != nil {
		t.Fatal(err)
	}
	if got, err := run("current", "-db", db, "-name", "portfolio"); err != nil || got != "none" {
		t.Fatalf("revoked current: %s %v", got, err)
	}
}

func TestConnectorCommandLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "state.db")
	cert := writeCert(t, dir)
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runConnector(ctx, args, &out)
		return out.String(), err
	}

	if _, err := run("list", "-db", db); err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("unmigrated database must be refused, got %v", err)
	}
	if err := runMigrate(ctx, []string{"-db", db}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if out, err := run("enroll", "-db", db, "-name", "portfolio", "-cert", cert); err != nil || !strings.Contains(out, "enrolled portfolio") {
		t.Fatalf("enroll: %q %v", out, err)
	}
	if _, err := run("enroll", "-db", db, "-name", "portfolio", "-cert", cert); err == nil {
		t.Fatal("duplicate enrolment accepted")
	}
	if out, _ := run("list", "-db", db); !strings.Contains(out, "portfolio") || !strings.Contains(out, "active") {
		t.Fatalf("list: %q", out)
	}
	if _, err := run("revoke", "-db", db, "-name", "portfolio"); err != nil {
		t.Fatal(err)
	}
	if out, _ := run("list", "-db", db); !strings.Contains(out, "revoked") {
		t.Fatalf("list after revoke: %q", out)
	}
}

func TestConnectorEnrollRejectsPrivateKeyFile(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "k.pem")
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")}), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fingerprintFromPEM(key); err == nil {
		t.Fatal("a private key file was accepted as a certificate")
	}
}
