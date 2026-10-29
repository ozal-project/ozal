package tunnelrenew

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// CreateAuthority replaces the retired pilot's create-ca.sh. It creates a
// new tunnel-only trust anchor; it never deploys or rotates trust on any host.
func CreateAuthority(ctx context.Context, directory string, days int, now time.Time) error {
	if days < 38 || days > 3650 {
		return errors.New("tunnel CA lifetime must be 38-3650 days")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return errors.New("tunnel CA needs a new private directory")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.New("cannot generate tunnel CA signer")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Ozal tunnel CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(time.Duration(days) * day), IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, public, key)
	if err != nil {
		return errors.New("cannot create tunnel CA certificate")
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeNew(filepath.Join(directory, "ca.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})); err != nil {
		return errors.New("cannot save private tunnel CA signer")
	}
	if err := writeNew(filepath.Join(directory, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return errors.New("cannot save public tunnel CA certificate")
	}
	complete = true
	return nil
}

func validRole(role string) bool { return role == "edge" || role == "connector" }

func PrepareRole(ctx context.Context, role, directory string) error {
	if !validRole(role) {
		return errors.New("identity role must be edge or connector")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return errors.New("identity preparation needs a new private directory")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()
	if _, err := generate(role, directory); err != nil {
		return errors.New("cannot prepare local tunnel identity")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	complete = true
	return nil
}

func SignRole(ctx context.Context, role, caDirectory, csrPath, output string) error {
	if !validRole(role) {
		return errors.New("identity role must be edge or connector")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.Open(csrPath)
	if err != nil {
		return errors.New("cannot open public role CSR")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return errors.New("CSR must be a bounded regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return errors.New("cannot read public role CSR")
	}
	if len(raw) > 64<<10 {
		return errors.New("role CSR is too large")
	}
	signed, err := issue(caDirectory, role, string(raw), time.Now())
	if err != nil {
		return errors.New("role signing failed; check CSR, CA role and lifetime")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeNew(output, []byte(signed)); err != nil {
		return errors.New("certificate output must be a new writable file")
	}
	return nil
}

func CheckRole(ctx context.Context, role, directory, caPath string) error {
	if !validRole(role) {
		return errors.New("identity role must be edge or connector")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return validate(role, directory, caPath, time.Now())
}
