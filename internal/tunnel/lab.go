package tunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// InitLab creates a new directory of short-lived throwaway identities. The CA
// key is never persisted. These fixtures are not an enrolment/CA service.
func InitLab(dir string) error {
	if err := os.Mkdir(dir, 0700); err != nil {
		return errors.New("create new lab identity directory (must not exist)")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(dir)
		}
	}()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Switchboard local spike CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		return err
	}
	if err := writePEM(filepath.Join(dir, "ca.crt"), "CERTIFICATE", caDER); err != nil {
		return err
	}
	for i, role := range []string{"edge", "connector"} {
		pub, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		usage := x509.ExtKeyUsageServerAuth
		if role == "connector" {
			usage = x509.ExtKeyUsageClientAuth
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, DNSNames: []string{role + ".localhost"}}
		der, err := x509.CreateCertificate(rand.Reader, cert, ca, pub, key)
		if err != nil {
			return err
		}
		if err := writePEM(filepath.Join(dir, role+".crt"), "CERTIFICATE", der); err != nil {
			return err
		}
		pk, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			return err
		}
		if err := writePEM(filepath.Join(dir, role+".key"), "PRIVATE KEY", pk); err != nil {
			return err
		}
	}
	complete = true
	return nil
}

func writePEM(path, kind string, der []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: kind, Bytes: der}); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
