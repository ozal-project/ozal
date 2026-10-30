package originauth

import (
	"crypto/tls"
	"crypto/x509"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ozal-project/ozal/internal/events"
)

// Trust is the set of authorities the public listener accepts client
// certificates from: the operator's configured bundle, plus the edge's own
// origin identity once it exists (EDGE-009). Readers take the current pool
// without locking; Add swaps in a new pool, so a handshake never sees a pool
// being modified. Nothing is ever removed at runtime: dropping an authority
// is a configuration change and a restart.
type Trust struct {
	mu   sync.Mutex
	pool atomic.Pointer[x509.CertPool]
}

// NewTrust starts from the configured bundle. base is cloned: later
// additions never change a pool the caller still holds.
func NewTrust(base *x509.CertPool) *Trust {
	t := &Trust{}
	t.pool.Store(base.Clone())
	return t
}

// Pool returns the authorities in effect now.
func (t *Trust) Pool() *x509.CertPool { return t.pool.Load() }

// Add trusts one more authority for every later handshake.
func (t *Trust) Add(ca *x509.Certificate) {
	t.mu.Lock()
	defer t.mu.Unlock()
	next := t.pool.Load().Clone()
	next.AddCert(ca)
	t.pool.Store(next)
}

// Trusts reports whether a client certificate chain would pass the policy
// now: the zone setup uses it to tell a proxy certificate this edge accepts
// from one it would refuse.
func (t *Trust) Trusts(chain []*x509.Certificate, now time.Time) bool {
	return Decide(chain, t.Pool(), now).Allowed
}

// ServerTLSTrust is ServerTLS reading its authorities from trust on every
// handshake, so an authority added after startup applies to the next
// connection without a restart.
func ServerTLSTrust(base *tls.Config, trust *Trust, sink events.Sink, opts ...Option) *tls.Config {
	return serverTLS(base, trust.Pool, sink, opts...)
}
