//go:build linux

package tunnelrenew

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCA(t *testing.T, home string, lifetime time.Duration) string {
	t.Helper()
	dir := filepath.Join(home, ".local/share/switchboard-tunnel-ca")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Disposable test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(lifetime), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeNew(filepath.Join(dir, "ca.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})); err != nil {
		t.Fatal(err)
	}
	if err := writeNew(filepath.Join(dir, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		t.Fatal(err)
	}
	return dir
}

func testAgent(t *testing.T) (Agent, string) {
	t.Helper()
	home := t.TempDir()
	ca := testCA(t, home, 365*day)
	a := Agent{Home: home, Role: "connector"}
	if err := os.MkdirAll(filepath.Join(a.base(), "identity"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(ca, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeNew(filepath.Join(a.base(), "identity/ca.crt"), data); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("identity", filepath.Join(a.base(), "current")); err != nil {
		t.Fatal(err)
	}
	return a, ca
}

func TestNativeIssuanceAndLocalKeyValidation(t *testing.T) {
	a, ca := testAgent(t)
	ctx := context.Background()
	id, _ := nonce()
	prepared, err := a.Handle(ctx, Request{Action: "prepare", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issue(ca, "connector", prepared.CSR, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	certified, err := a.Handle(ctx, Request{Action: "certify", Link: prepared.Identity.Link, Certificate: issued})
	if err != nil {
		t.Fatal(err)
	}
	if len(certified.Identity.Fingerprint) != 64 || time.Until(certified.Identity.Expires) < 29*day {
		t.Fatal("invalid leaf lifetime or fingerprint")
	}
	info, err := os.Stat(filepath.Join(a.base(), prepared.Identity.Link, "connector.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("private key permissions")
	}
}

func TestRejectsSignedCertificateForDifferentKey(t *testing.T) {
	a, ca := testAgent(t)
	ctx := context.Background()
	id, _ := nonce()
	first, err := a.Handle(ctx, Request{Action: "prepare", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	id, _ = nonce()
	second, err := a.Handle(ctx, Request{Action: "prepare", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issue(ca, "connector", first.CSR, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Handle(ctx, Request{Action: "certify", Link: second.Identity.Link, Certificate: issued}); err == nil {
		t.Fatal("mismatching key accepted")
	}
	if current, err := a.current(); err != nil || current != "identity" {
		t.Fatal("failed certification changed current")
	}
}

func TestCARolloverDueAndInvalidCSRFailBeforeIssuance(t *testing.T) {
	ca := testCA(t, t.TempDir(), 35*day)
	if _, err := issue(ca, "connector", "unused", time.Now()); err == nil || !strings.Contains(err.Error(), "CA expires") {
		t.Fatalf("CA guard: %v", err)
	}
	ca = testCA(t, t.TempDir(), 365*day)
	if _, err := issue(ca, "connector", "invalid", time.Now()); err == nil {
		t.Fatal("invalid CSR accepted")
	}
}

func TestPendingJournalSurvivesUnavailableVPS(t *testing.T) {
	home := t.TempDir()
	testCA(t, home, 365*day)
	dir := filepath.Join(home, "runtime")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	h, txs := fixture()
	journal := filepath.Join(dir, "pending.json")
	if err := save(journal, pending{Rotations: txs}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(journal)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("journal not private")
	}
	h.offline = true
	if err := renew(context.Background(), h, home, dir, false, io.Discard); err == nil {
		t.Fatal("offline recovery passed")
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatal("lost pending journal")
	}
	h.offline = false
	if err := renew(context.Background(), h, home, dir, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatal("completed journal still exists")
	}
}
