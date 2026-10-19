package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/events"
)

// Default timing for the renewal loop. The first failure backs off a couple
// of minutes and doubles from there: Let's Encrypt counts five failed
// validations per account and hostname per hour, so retrying faster than
// this would spend the budget instead of waiting for a fix.
const (
	defaultAttemptTimeout = 5 * time.Minute
	defaultBackoffBase    = 2 * time.Minute
	defaultBackoffMax     = time.Hour
)

// ManagerConfig configures the certificate manager. Test callers inject Now
// and Jitter and shrink the timing fields to keep the loop deterministic.
type ManagerConfig struct {
	// Host is the single certificate name this manager serves: a hostname, or
	// a single-level wildcard such as "*.example.com" (see names.go).
	Host string
	// RootCAs overrides system certificate roots, for explicit staging trust.
	RootCAs *x509.CertPool
	// AttemptTimeout bounds one issuance attempt, DNS waits included.
	AttemptTimeout time.Duration
	// BackoffBase is the delay after the first failed attempt; every further
	// consecutive failure doubles it up to BackoffMax.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// Now returns the current time; nil selects time.Now.
	Now func() time.Time
	// Jitter spreads retries; nil selects a value in [d/2, d).
	Jitter func(time.Duration) time.Duration
	Logger *slog.Logger
	Sink   events.Sink
}

// Manager owns the origin certificate: it serves the newest valid
// generation through GetCertificate, keeps at most one issuance attempt
// running, and schedules the next attempt from the certificate's actual
// lifetime. With no valid certificate it fails closed: handshakes are
// refused until an attempt succeeds.
type Manager struct {
	cfg    ManagerConfig
	store  Store
	issuer Issuer

	current    atomic.Pointer[activeCertificate]
	attempting atomic.Bool

	mu             sync.Mutex
	lastOutcome    string
	lastError      string
	lastAttempt    time.Time
	nextAttempt    time.Time
	failures       int
	pendingIntents int
}

type activeCertificate struct {
	cert *tls.Certificate
	leaf *x509.Certificate
}

// NewManager validates its dependencies and applies the timing defaults.
func NewManager(cfg ManagerConfig, store Store, issuer Issuer) (*Manager, error) {
	if cfg.Host == "" {
		return nil, errors.New("certs: manager requires a host")
	}
	if store == nil {
		return nil, errors.New("certs: manager requires a store")
	}
	if issuer == nil {
		return nil, errors.New("certs: manager requires an issuer")
	}
	if cfg.RootCAs == nil {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("certs: load system certificate roots: %w", err)
		}
		cfg.RootCAs = roots
	} else {
		cfg.RootCAs = cfg.RootCAs.Clone()
	}
	if cfg.AttemptTimeout <= 0 {
		cfg.AttemptTimeout = defaultAttemptTimeout
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = defaultBackoffBase
	}
	if cfg.BackoffMax < cfg.BackoffBase {
		cfg.BackoffMax = defaultBackoffMax
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Jitter == nil {
		cfg.Jitter = func(d time.Duration) time.Duration {
			half := d / 2
			if half <= 0 {
				return d
			}
			return half + time.Duration(mrand.Int64N(int64(half)))
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Sink == nil {
		cfg.Sink = events.Discard
	}
	return &Manager{cfg: cfg, store: store, issuer: issuer}, nil
}

// GetCertificate is the only method the public listener needs; it is
// declared by the listener as a one-method interface and wired into
// tls.Config.GetCertificate by the composition root. It never blocks on
// issuance: the renewal loop runs separately, and a missing or invalid
// certificate is refused instead of served.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if hello == nil {
		return nil, errors.New("certs: no client hello")
	}
	if !Covers(m.cfg.Host, hello.ServerName) {
		return nil, fmt.Errorf("certs: this listener serves %q, not %q", m.cfg.Host, hello.ServerName)
	}
	active := m.current.Load()
	if active == nil {
		return nil, errNotConfigured
	}
	now := m.now()
	if now.Before(active.leaf.NotBefore) || !now.Before(active.leaf.NotAfter) {
		return nil, errors.New("certs: the stored certificate is outside its validity window")
	}
	return active.cert, nil
}

// Run owns the renewal loop until ctx is cancelled. It first recovers
// persisted state — the last complete generation and any DNS cleanup left
// behind — then attempts issuance whenever one is due, with backoff on
// failure. cmd/switchboard runs it in a goroutine and joins it through the
// same context that stops the listeners.
func (m *Manager) Run(ctx context.Context) {
	m.startup(ctx)
	for {
		if ctx.Err() != nil {
			return
		}
		if wait := m.waitFor(m.now()); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		m.attempt(ctx)
	}
}

// startup reloads the last complete generation and reconciles DNS state.
// A stored certificate that fails validation is reported and ignored, never
// served.
func (m *Manager) startup(ctx context.Context) {
	if gen, ok, err := m.store.Load(ctx, m.cfg.Host); err != nil {
		m.logger().Warn("certificate state could not be read", "host", m.cfg.Host, "error", err)
	} else if ok {
		if cert, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, m.cfg.Host, m.now(), m.cfg.RootCAs); err != nil {
			m.logger().Warn("stored certificate is not usable", "host", m.cfg.Host, "error", err)
		} else {
			m.current.Store(&activeCertificate{cert: &cert, leaf: cert.Leaf})
			m.logger().Info("loaded stored certificate",
				"host", m.cfg.Host,
				"not_after", cert.Leaf.NotAfter.UTC().Format(time.RFC3339),
			)
		}
	}
	pending, err := m.issuer.Recover(ctx)
	m.mu.Lock()
	m.pendingIntents = pending
	m.mu.Unlock()
	if pending > 0 {
		m.logger().Warn("challenge records from earlier attempts still need cleanup", "pending", pending)
	}
	if err != nil {
		m.logger().Warn("dns cleanup recovery did not finish", "error", err)
	}
}

// waitFor returns how long to wait before the next attempt: zero when one
// is due now.
func (m *Manager) waitFor(now time.Time) time.Duration {
	m.mu.Lock()
	next := m.nextAttempt
	m.mu.Unlock()
	if next.After(now) {
		return next.Sub(now)
	}
	active := m.current.Load()
	if active == nil {
		return 0
	}
	if RenewalDue(now, active.leaf.NotBefore, active.leaf.NotAfter) {
		return 0
	}
	boundary := active.leaf.NotAfter.Add(-active.leaf.NotAfter.Sub(active.leaf.NotBefore) / 3)
	return boundary.Sub(now)
}

// attempt runs exactly one issuance attempt unless another one is already
// running. Only the newest complete generation is ever activated: the
// current certificate keeps being served until a validated replacement is
// durably stored.
func (m *Manager) attempt(ctx context.Context) {
	if !m.attempting.CompareAndSwap(false, true) {
		return
	}
	defer m.attempting.Store(false)

	attemptCtx, cancel := context.WithTimeout(ctx, m.cfg.AttemptTimeout)
	defer cancel()

	started := m.now()
	key, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		m.recordFailure(started, fmt.Errorf("certs: generate certificate key: %w", err))
		return
	}
	gen, err := m.issuer.Obtain(attemptCtx, m.cfg.Host, key)
	if err != nil {
		m.recordFailure(started, err)
		return
	}
	cert, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, m.cfg.Host, m.now(), m.cfg.RootCAs)
	if err != nil {
		m.recordFailure(started, err)
		return
	}
	if err := m.store.Save(attemptCtx, m.cfg.Host, gen); err != nil {
		m.recordFailure(started, err)
		return
	}
	m.current.Store(&activeCertificate{cert: &cert, leaf: cert.Leaf})
	m.recordSuccess(started, cert.Leaf)
}

func (m *Manager) recordSuccess(at time.Time, leaf *x509.Certificate) {
	next := leaf.NotAfter.Add(-leaf.NotAfter.Sub(leaf.NotBefore) / 3)
	if !next.After(at) {
		// A certificate that is already due again must never spin the loop.
		next = at.Add(m.cfg.BackoffBase)
	}
	m.mu.Lock()
	m.failures = 0
	m.lastOutcome = "renewed"
	m.lastError = ""
	m.lastAttempt = at
	m.nextAttempt = next
	m.mu.Unlock()

	m.logger().Info("origin certificate renewed",
		"host", m.cfg.Host,
		"not_before", leaf.NotBefore.UTC().Format(time.RFC3339),
		"not_after", leaf.NotAfter.UTC().Format(time.RFC3339),
		"next_attempt", next.UTC().Format(time.RFC3339),
	)
	m.emit(events.Event{
		Time:   at,
		Type:   CertRenewed,
		Host:   m.cfg.Host,
		Code:   "renewed",
		Reason: "not_after=" + leaf.NotAfter.UTC().Format(time.RFC3339),
	})
}

func (m *Manager) recordFailure(at time.Time, err error) {
	m.mu.Lock()
	m.failures++
	delay := m.backoffDelay(err, m.failures)
	m.lastOutcome = "failed"
	m.lastError = err.Error()
	m.lastAttempt = at
	m.nextAttempt = at.Add(delay)
	m.mu.Unlock()

	m.logger().Warn("origin certificate renewal failed",
		"host", m.cfg.Host,
		"code", failureCode(err),
		"error", err,
		"next_attempt", at.Add(delay).UTC().Format(time.RFC3339),
	)
	// The event carries the stable code and the next attempt, never the raw
	// error: a challenge value or a token must not reach an event.
	m.emit(events.Event{
		Time:   at,
		Type:   CertRenewalFailed,
		Host:   m.cfg.Host,
		Code:   failureCode(err),
		Reason: "next_attempt=" + at.Add(delay).UTC().Format(time.RFC3339),
	})
}

// backoffDelay is bounded exponential backoff with jitter, overridden by
// the server's own rate-limit figure when there is one.
func (m *Manager) backoffDelay(err error, failures int) time.Duration {
	var limited *RateLimitedError
	if errors.As(err, &limited) && limited.RetryAfter > 0 {
		return limited.RetryAfter
	}
	shift := min(failures-1, 20)
	delay := m.cfg.BackoffBase << shift
	if delay <= 0 || delay > m.cfg.BackoffMax {
		delay = m.cfg.BackoffMax
	}
	return m.cfg.Jitter(delay)
}

// Status is the manager's observable state for the trust console.
type Status struct {
	Host           string
	Valid          bool
	NotBefore      time.Time
	NotAfter       time.Time
	LastOutcome    string
	LastError      string
	LastAttempt    time.Time
	NextAttempt    time.Time
	PendingIntents int
}

// Status reports the current generation and the last/next attempt.
func (m *Manager) Status() Status {
	status := Status{Host: m.cfg.Host}
	now := m.now()
	if active := m.current.Load(); active != nil {
		status.Valid = !now.Before(active.leaf.NotBefore) && now.Before(active.leaf.NotAfter)
		status.NotBefore = active.leaf.NotBefore
		status.NotAfter = active.leaf.NotAfter
	}
	m.mu.Lock()
	status.LastOutcome = m.lastOutcome
	status.LastError = m.lastError
	status.LastAttempt = m.lastAttempt
	status.NextAttempt = m.nextAttempt
	status.PendingIntents = m.pendingIntents
	m.mu.Unlock()
	return status
}

func (m *Manager) now() time.Time { return m.cfg.Now() }

func (m *Manager) logger() *slog.Logger { return m.cfg.Logger }

func (m *Manager) emit(e events.Event) { m.cfg.Sink.Emit(e) }

// failureCode maps an error to a stable, safe event code.
func failureCode(err error) string {
	var limited *RateLimitedError
	switch {
	case errors.As(err, &limited):
		return "rate_limited"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrDNS):
		return "dns"
	case errors.Is(err, ErrStorage):
		return "storage"
	case errors.Is(err, ErrInvalidGeneration):
		return "invalid_certificate"
	default:
		return "acme"
	}
}
