package certs

import (
	"context"
	"crypto"
	"crypto/tls"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/events"
)

// stubIssuer scripts issuance outcomes and counts calls, so the manager's
// scheduling can be tested without any network or clock dependency.
type stubIssuer struct {
	mu      sync.Mutex
	calls   int
	obtain  func(ctx context.Context, names []string, key crypto.Signer) (StoredGeneration, error)
	recover func(ctx context.Context) (int, error)
	started chan struct{}
	release chan struct{}
}

func (s *stubIssuer) Obtain(ctx context.Context, names []string, key crypto.Signer) (StoredGeneration, error) {
	s.mu.Lock()
	s.calls++
	started, release, obtain := s.started, s.release, s.obtain
	s.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return StoredGeneration{}, ctx.Err()
		}
	}
	if obtain == nil {
		return StoredGeneration{}, errors.New("stub issuer: no outcome configured")
	}
	return obtain(ctx, names, key)
}

func (s *stubIssuer) Recover(ctx context.Context) (int, error) {
	if s.recover != nil {
		return s.recover(ctx)
	}
	return 0, nil
}

func (s *stubIssuer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func testManager(t *testing.T, store Store, issuer Issuer, now time.Time, trusted ...StoredGeneration) *Manager {
	t.Helper()
	m, err := NewManager(ManagerConfig{
		Host:           "preview.example.com",
		RootCAs:        testRoots(t, trusted...),
		Now:            func() time.Time { return now },
		Jitter:         func(d time.Duration) time.Duration { return d },
		AttemptTimeout: time.Minute,
		BackoffBase:    time.Minute,
		BackoffMax:     time.Hour,
	}, store, issuer)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestManagerServesPersistedCertificateAfterRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	store := testStore(t)
	gen, leaf := testGeneration(t, "preview.example.com", now.Add(-time.Hour), now.Add(90*24*time.Hour))
	if err := store.Save(ctx, "preview.example.com", gen); err != nil {
		t.Fatalf("Save: %v", err)
	}
	issuer := &stubIssuer{}
	m := testManager(t, store, issuer, now, gen)

	// startup is what Run does first: reload state, reconcile DNS.
	m.startup(ctx)
	if issuer.callCount() != 0 {
		t.Fatalf("a fresh certificate must not trigger issuance, got %d calls", issuer.callCount())
	}

	cert, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "preview.example.com"})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if !cert.Leaf.NotAfter.Equal(leaf.NotAfter) {
		t.Fatalf("served NotAfter = %s, want %s", cert.Leaf.NotAfter, leaf.NotAfter)
	}
	if status := m.Status(); !status.Valid || status.NotAfter.IsZero() {
		t.Fatalf("status = %+v", status)
	}
}

func TestManagerFailsClosedWithoutCertificate(t *testing.T) {
	now := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	m := testManager(t, testStore(t), &stubIssuer{}, now)
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "preview.example.com"}); err == nil {
		t.Fatal("without a certificate the handshake must fail closed")
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "attacker.example.com"}); err == nil {
		t.Fatal("another hostname must be refused")
	}
	if status := m.Status(); status.Valid {
		t.Fatalf("status must not report a valid certificate: %+v", status)
	}
}

func TestManagerRenewsWhenDueAndEmitsEvent(t *testing.T) {
	now := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	start := now.Add(-60 * 24 * time.Hour) // 60 days used of a 90-day lifetime: inside the last third
	store := testStore(t)
	old, _ := testGeneration(t, "preview.example.com", start, start.Add(90*24*time.Hour))
	if err := store.Save(context.Background(), "preview.example.com", old); err != nil {
		t.Fatal(err)
	}
	fresh, freshLeaf := testGeneration(t, "preview.example.com", now.Add(-time.Minute), now.Add(90*24*time.Hour))
	issuer := &stubIssuer{obtain: func(context.Context, []string, crypto.Signer) (StoredGeneration, error) {
		return fresh, nil
	}}
	recorder := &events.Recorder{}
	m, err := NewManager(ManagerConfig{
		Host:    "preview.example.com",
		RootCAs: testRoots(t, old, fresh),
		Now:     func() time.Time { return now },
		Jitter:  func(d time.Duration) time.Duration { return d },
		Sink:    recorder,
	}, store, issuer)
	if err != nil {
		t.Fatal(err)
	}
	m.startup(context.Background())
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "preview.example.com"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(5 * time.Second)
	for {
		cert, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "preview.example.com"})
		if err == nil && cert.Leaf.NotAfter.Equal(freshLeaf.NotAfter) && recorder.Has(CertRenewed) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("certificate was not renewed in time (status %+v)", m.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !recorder.Has(CertRenewed) {
		t.Fatal("cert.renewed was not emitted")
	}
	// A renewed certificate must also be durably stored.
	loaded, ok, err := store.Load(context.Background(), "preview.example.com")
	if err != nil || !ok {
		t.Fatalf("Load after renewal = ok %v, err %v", ok, err)
	}
	if string(loaded.CertPEM) != string(fresh.CertPEM) {
		t.Fatal("the renewed generation was not persisted")
	}
}

func TestManagerKeepsServingAfterFailedRenewal(t *testing.T) {
	now := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	start := now.Add(-60 * 24 * time.Hour)
	store := testStore(t)
	old, oldLeaf := testGeneration(t, "preview.example.com", start, start.Add(90*24*time.Hour))
	if err := store.Save(context.Background(), "preview.example.com", old); err != nil {
		t.Fatal(err)
	}
	issuer := &stubIssuer{obtain: func(context.Context, []string, crypto.Signer) (StoredGeneration, error) {
		return StoredGeneration{}, errors.New("acme: simulated outage")
	}}
	recorder := &events.Recorder{}
	m, err := NewManager(ManagerConfig{
		Host:        "preview.example.com",
		RootCAs:     testRoots(t, old),
		Now:         func() time.Time { return now },
		Jitter:      func(d time.Duration) time.Duration { return d },
		BackoffBase: 5 * time.Minute,
		BackoffMax:  time.Hour,
		Sink:        recorder,
	}, store, issuer)
	if err != nil {
		t.Fatal(err)
	}
	m.startup(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(5 * time.Second)
	for {
		if status := m.Status(); status.LastOutcome == "failed" && recorder.Has(CertRenewalFailed) {
			if !status.NextAttempt.Equal(now.Add(5 * time.Minute)) {
				t.Fatalf("next attempt = %s, want %s (backoff base)", status.NextAttempt, now.Add(5*time.Minute))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no failure recorded (status %+v)", m.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The still-valid certificate keeps being served and the event is safe.
	cert, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "preview.example.com"})
	if err != nil || !cert.Leaf.NotAfter.Equal(oldLeaf.NotAfter) {
		t.Fatalf("old certificate not retained: cert %v, err %v", cert, err)
	}
	event, ok := recorder.Find(CertRenewalFailed)
	if !ok {
		t.Fatal("cert.renewal_failed was not emitted")
	}
	if event.Code != "acme" {
		t.Fatalf("failure code = %q, want acme", event.Code)
	}
	if event.Error != "" {
		t.Fatalf("the event must not carry the raw error text, got %q", event.Error)
	}
}

func TestManagerHonorsRateLimitRetryAfter(t *testing.T) {
	now := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	issuer := &stubIssuer{obtain: func(context.Context, []string, crypto.Signer) (StoredGeneration, error) {
		return StoredGeneration{}, &RateLimitedError{RetryAfter: 45 * time.Minute, Err: errors.New("acme: rate limited")}
	}}
	m := testManager(t, testStore(t), issuer, now)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(5 * time.Second)
	for {
		status := m.Status()
		if status.LastOutcome == "failed" {
			if !status.NextAttempt.Equal(now.Add(45 * time.Minute)) {
				t.Fatalf("next attempt = %s, want the server's retry-after (%s)", status.NextAttempt, now.Add(45*time.Minute))
			}
			if status.LastError == "" {
				t.Fatal("the failure reason must be visible in the console")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rate-limited attempt was not recorded in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestManagerOnlyOneActiveAttempt(t *testing.T) {
	now := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	fresh, _ := testGeneration(t, "preview.example.com", now.Add(-time.Minute), now.Add(90*24*time.Hour))
	issuer := &stubIssuer{
		started: make(chan struct{}),
		release: make(chan struct{}),
		obtain: func(context.Context, []string, crypto.Signer) (StoredGeneration, error) {
			return fresh, nil
		},
	}
	m := testManager(t, testStore(t), issuer, now, fresh)

	first := make(chan struct{})
	go func() { m.attempt(context.Background()); close(first) }()
	<-issuer.started
	m.attempt(context.Background()) // must return immediately: an attempt is active
	close(issuer.release)
	<-first

	if calls := issuer.callCount(); calls != 1 {
		t.Fatalf("issuer called %d times, want exactly one active attempt", calls)
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "preview.example.com"}); err != nil {
		t.Fatalf("after a successful attempt the certificate must be served: %v", err)
	}
}

// countingStore counts loads, to show a certificate is read from disk once.
type countingStore struct {
	Store
	mu    sync.Mutex
	loads int
}

func (s *countingStore) Load(ctx context.Context, host string) (StoredGeneration, bool, error) {
	s.mu.Lock()
	s.loads++
	s.mu.Unlock()
	return s.Store.Load(ctx, host)
}

// The listener checks Status right after it opens: the stored certificate
// must already be active then, before the renewal loop has started.
func TestLoadStoredActivatesBeforeRunAndOnlyOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 21, 0, 0, 0, 0, time.UTC)
	files := testStore(t)
	gen, _ := testGeneration(t, "preview.example.com", now.Add(-time.Hour), now.Add(90*24*time.Hour))
	if err := files.Save(ctx, "preview.example.com", gen); err != nil {
		t.Fatalf("Save: %v", err)
	}
	store := &countingStore{Store: files}
	recovered := make(chan struct{}, 1)
	issuer := &stubIssuer{recover: func(context.Context) (int, error) { recovered <- struct{}{}; return 0, nil }}
	m := testManager(t, store, issuer, now, gen)

	if m.Status().Valid {
		t.Fatal("nothing is active before LoadStored")
	}
	m.LoadStored(ctx)
	if !m.Status().Valid {
		t.Fatal("LoadStored must activate the stored certificate synchronously")
	}
	select {
	case <-recovered:
		t.Fatal("LoadStored must not reach the DNS provider: recovery belongs to Run")
	default:
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(runCtx) }()
	<-recovered
	cancel()
	<-done
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loads != 1 {
		t.Errorf("certificate read %d times, want once", store.loads)
	}
}
