package tunnel

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
)

// FingerprintFromPEM returns the fingerprint of the first CERTIFICATE block.
// Every block is checked, not just the first: a private key anywhere in the
// input is rejected so a mistaken argument cannot leave a key being read or
// sent. Later certificates (a chain) must be valid but are not enrolled.
func FingerprintFromPEM(data []byte) (Fingerprint, error) {
	var fp Fingerprint
	found := false
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return Fingerprint{}, errors.New("file must contain only the public certificate")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return Fingerprint{}, errors.New("invalid connector certificate")
		}
		if !found {
			fp, found = FingerprintOf(block.Bytes), true
		}
	}
	if !found {
		return Fingerprint{}, errors.New("no certificate found in file")
	}
	return fp, nil
}
