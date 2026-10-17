// Package tunnel implements the local TUN-001 experiment. It is not a public
// deployment configuration: the CLI enforces loopback at both ends.
package tunnel

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"slices"
)

const ALPN = "switchboard-tunnel-spike/1"

// AuthorizeConnector is deliberately exact: a certificate from our CA does
// not authorize every connector, and wildcard DNS identities are not accepted.
func AuthorizeConnector(names []string, expected string) bool {
	return expected != "" && slices.Contains(names, expected)
}

func LoadIdentity(certPath, keyPath, caPath string) (tls.Certificate, *x509.CertPool, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, nil, errors.New("load tunnel certificate/key")
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return tls.Certificate{}, nil, errors.New("load tunnel CA")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return tls.Certificate{}, nil, errors.New("invalid tunnel CA")
	}
	return cert, roots, nil
}

func ServerTLS(cert tls.Certificate, roots *x509.CertPool, expected string) (*tls.Config, error) {
	if roots == nil || len(cert.Certificate) == 0 || expected == "" {
		return nil, errors.New("tunnel requires a certificate, CA and connector identity")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert},
		ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, NextProtos: []string{ALPN},
		VerifyConnection: func(state tls.ConnectionState) error {
			if state.NegotiatedProtocol != ALPN || len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 || !AuthorizeConnector(state.PeerCertificates[0].DNSNames, expected) {
				return errors.New("connector identity denied")
			}
			return nil
		},
	}, nil
}

func ClientTLS(cert tls.Certificate, roots *x509.CertPool, serverName string) (*tls.Config, error) {
	if roots == nil || len(cert.Certificate) == 0 || serverName == "" {
		return nil, errors.New("tunnel requires a certificate, CA and server identity")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, RootCAs: roots,
		ServerName: serverName, NextProtos: []string{ALPN},
		VerifyConnection: func(state tls.ConnectionState) error {
			if state.NegotiatedProtocol != ALPN {
				return errors.New("tunnel protocol mismatch")
			}
			return nil
		},
	}, nil
}

// ServerTLSRegistry is ServerTLS for registry mode: the TLS layer proves the
// client certificate chains to our CA with client-auth EKU and the right ALPN;
// whether that certificate is an enrolled connector is decided afterwards by
// the Edge's Authorizer, which can consult the database and name the peer.
func ServerTLSRegistry(cert tls.Certificate, roots *x509.CertPool) (*tls.Config, error) {
	if roots == nil || len(cert.Certificate) == 0 {
		return nil, errors.New("tunnel requires a certificate and CA")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert},
		ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, NextProtos: []string{ALPN},
		VerifyConnection: func(state tls.ConnectionState) error {
			if state.NegotiatedProtocol != ALPN || len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return errors.New("connector identity denied")
			}
			return nil
		},
	}, nil
}
