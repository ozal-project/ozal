// Package originauth makes the public HTTPS listener serve only connections
// that present a client certificate from a configured trust bundle.
//
// It is how Switchboard recognises the proxy in front of it (Cloudflare
// Authenticated Origin Pulls with the zone's own certificate): source IP
// filtering identifies a network, a client certificate identifies our
// deployment. Decide is the pure core; ServerTLS and RequireVerified are the
// adapters that apply it to a listener.
package originauth

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/switchboard-project/switchboard/internal/events"
)

// Code is a stable decision code; it appears in events and logs.
type Code string

const (
	Allowed       Code = "origin.allowed"
	NoCertificate Code = "origin.no_certificate"
	Untrusted     Code = "origin.untrusted"
	Expired       Code = "origin.expired"
	Invalid       Code = "origin.invalid"
)

// OriginDenied is emitted for every client certificate the policy refuses.
const OriginDenied events.Type = "origin.denied"

// acmeALPN is the protocol of ACME tls-alpn-01 validation.
const acmeALPN = "acme-tls/1"

// Option adjusts the client-certificate policy.
type Option func(*options)

type options struct {
	allowACMEValidation bool
}

// AllowACMEValidation keeps the tls-alpn-01 exemption: the exact ACME
// validation exchange skips the client-certificate policy because it cannot
// present one. It belongs to listeners that may answer autocert's TLS-ALPN
// challenge. A DNS-01 listener never receives such a connection and must
// NOT set it — there the exemption is an open door.
func AllowACMEValidation() Option {
	return func(o *options) { o.allowACMEValidation = true }
}

// Decision is the outcome of checking one client certificate chain.
type Decision struct {
	Allowed bool
	Code    Code
	Reason  string
}

// Decide checks a presented chain against the trust bundle. peers[0] is the
// leaf and the rest are intermediates the client sent. The reasons are fixed
// strings: nothing from the peer's certificate is echoed back.
func Decide(peers []*x509.Certificate, roots *x509.CertPool, now time.Time) Decision {
	if len(peers) == 0 {
		return Decision{Code: NoCertificate, Reason: "no client certificate presented"}
	}
	intermediates := x509.NewCertPool()
	for _, c := range peers[1:] {
		intermediates.AddCert(c)
	}
	_, err := peers[0].Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err == nil {
		return Decision{Allowed: true, Code: Allowed}
	}
	var invalid x509.CertificateInvalidError
	var unknown x509.UnknownAuthorityError
	switch {
	case errors.As(err, &unknown):
		return Decision{Code: Untrusted, Reason: "client certificate is not issued by the configured authority"}
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		return Decision{Code: Expired, Reason: "client certificate is expired or not yet valid"}
	default:
		return Decision{Code: Invalid, Reason: "client certificate is not valid for client authentication"}
	}
}

// LoadPool parses a PEM trust bundle. An empty or unusable bundle is an error:
// a policy that trusts nothing must fail at startup, never silently accept or
// silently refuse everything.
func LoadPool(pemData []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, errors.New("origin client CA file contains no certificate")
	}
	return pool, nil
}

// denialWindow bounds repeated origin.denied events: a scan that never
// presents a valid certificate must not be able to flood the event log.
const denialWindow = time.Minute

// denialEmitter collapses identical denials inside one window into a single
// event whose Count reports how many were suppressed since the previous one.
// A denied handshake already fails closed; the event is for operations.
type denialEmitter struct {
	sink events.Sink
	now  func() time.Time

	mu          sync.Mutex
	windowStart time.Time
	suppressed  map[Code]int
}

func newDenialEmitter(sink events.Sink) *denialEmitter {
	if sink == nil {
		sink = events.Discard
	}
	return &denialEmitter{sink: sink, now: time.Now, suppressed: make(map[Code]int)}
}

func (d *denialEmitter) emit(code Code, reason, remote string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if d.windowStart.IsZero() || now.Sub(d.windowStart) >= denialWindow {
		count := d.suppressed[code]
		d.windowStart = now
		d.suppressed = make(map[Code]int)
		d.sink.Emit(events.Event{
			Time: now, Type: OriginDenied, Code: string(code),
			Reason: reason, RemoteAddr: remote, Count: count,
		})
		return
	}
	d.suppressed[code]++
}

// ServerTLS returns base with the client-certificate policy applied to every
// handshake. Denials are aggregated per minute and emitted to sink. Go
// verifies nothing itself here (RequestClientCert): VerifyConnection owns the
// decision so each refusal gets a code and an event. The policy is strict by
// default; only AllowACMEValidation re-opens the exact tls-alpn-01 validation
// exchange.
func ServerTLS(base *tls.Config, roots *x509.CertPool, sink events.Sink, opts ...Option) *tls.Config {
	denials := newDenialEmitter(sink)
	var cfgOpts options
	for _, opt := range opts {
		opt(&cfgOpts)
	}
	cfg := base.Clone()
	cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		// Only the exact validation exchange skips the policy, and only
		// when the listener opted in; a hello that also offers HTTP does
		// not. net/http would serve such a connection, so RequireVerified
		// closes that door at the request layer.
		if cfgOpts.allowACMEValidation && slices.Equal(hello.SupportedProtos, []string{acmeALPN}) {
			return nil, nil // use the unmodified base configuration
		}
		remote := ""
		if hello.Conn != nil {
			remote = hello.Conn.RemoteAddr().String()
		}
		strict := cfg.Clone()
		strict.GetConfigForClient = nil
		strict.MinVersion = max(strict.MinVersion, tls.VersionTLS12)
		strict.ClientAuth = tls.RequestClientCert
		strict.VerifyConnection = func(cs tls.ConnectionState) error {
			now := time.Now()
			d := Decide(cs.PeerCertificates, roots, now)
			if d.Allowed {
				return nil
			}
			denials.emit(d.Code, d.Reason, remote)
			return errors.New("origin client certificate rejected")
		}
		return strict, nil
	}
	return cfg
}

// RequireVerified refuses any request that did not arrive over a connection
// with a client certificate that passed the policy. It is the second lock: a
// handshake that skipped the policy (ACME) must never reach the routes.
func RequireVerified(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
