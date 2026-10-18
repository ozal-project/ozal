// Package tunnelrenew manages the Linux private pilot's leaf identities.
// Role keys are generated locally; only public CSRs/certificates cross hosts.
package tunnelrenew

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const day = 24 * time.Hour

type Identity struct {
	Link        string    `json:"link"`
	Certificate string    `json:"certificate"`
	Fingerprint string    `json:"fingerprint"`
	Expires     time.Time `json:"expires"`
}

func parseCertificate(data []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("expected one public certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func certificate(path string) (Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, err
	}
	cert, err := parseCertificate(data)
	if err != nil {
		return Identity{}, err
	}
	fp := sha256.Sum256(cert.Raw)
	return Identity{Certificate: string(data), Fingerprint: hex.EncodeToString(fp[:]), Expires: cert.NotAfter}, nil
}

func writeNew(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func generate(role, directory string) (string, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	if err := writeNew(filepath.Join(directory, role+".key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})); err != nil {
		return "", err
	}
	request, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: role + ".homelab.internal"}}, key)
	if err != nil {
		return "", err
	}
	public := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: request})
	if err := writeNew(filepath.Join(directory, role+".csr"), public); err != nil {
		return "", err
	}
	return string(public), nil
}

func issue(caDirectory, role, request string, now time.Time) (string, error) {
	caData, err := os.ReadFile(filepath.Join(caDirectory, "ca.crt"))
	if err != nil {
		return "", err
	}
	ca, err := parseCertificate(caData)
	if err != nil {
		return "", err
	}
	if ca.NotAfter.Sub(now) <= 37*day {
		return "", errors.New("CA expires within 37 days; planned trust-anchor rollover required")
	}
	keyData, err := os.ReadFile(filepath.Join(caDirectory, "ca.key"))
	if err != nil {
		return "", err
	}
	keyBlock, _ := pem.Decode(keyData)
	if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" {
		return "", errors.New("invalid local CA key format")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return "", errors.New("cannot load local CA signer")
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return "", errors.New("unsupported CA signer")
	}
	block, rest := pem.Decode([]byte(request))
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return "", errors.New("expected public CSR")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return "", err
	}
	if err := csr.CheckSignature(); err != nil {
		return "", errors.New("CSR signature invalid")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", err
	}
	usage := x509.ExtKeyUsageClientAuth
	if role == "edge" {
		usage = x509.ExtKeyUsageServerAuth
	} else if role != "connector" {
		return "", errors.New("invalid identity role")
	}
	leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: role + ".homelab.internal"}, DNSNames: []string{role + ".homelab.internal"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(30 * day), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, csr.PublicKey, signer)
	if err != nil {
		return "", errors.New("cannot sign role certificate")
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
}

func validate(role, directory, currentCA string, now time.Time) error {
	leafPath := filepath.Join(directory, role+".crt")
	if _, err := tls.LoadX509KeyPair(leafPath, filepath.Join(directory, role+".key")); err != nil {
		return errors.New("certificate does not match local private key")
	}
	data, err := os.ReadFile(leafPath)
	if err != nil {
		return err
	}
	leaf, err := parseCertificate(data)
	if err != nil {
		return err
	}
	ca, err := os.ReadFile(currentCA)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("invalid current CA")
	}
	name := role + ".homelab.internal"
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != name {
		return errors.New("unexpected certificate identity")
	}
	usage := x509.ExtKeyUsageClientAuth
	if role == "edge" {
		usage = x509.ExtKeyUsageServerAuth
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
		return fmt.Errorf("role certificate validation: %w", err)
	}
	if leaf.NotAfter.Sub(now) < 29*day {
		return errors.New("replacement lifetime is too short")
	}
	return nil
}
