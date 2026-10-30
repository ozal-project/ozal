package certs

import (
	"context"
	"crypto"
	"crypto/tls"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateZone(t *testing.T) {
	for zone, ok := range map[string]bool{
		"example.com":     true,
		"lab.example.com": true,
		"Example.com":     false,
		"example.com.":    false,
		"*.example.com":   false,
		"com":             false,
		"localhost":       false,
		"app.localhost":   false,
		"192.168.1.1":     false,
		"-bad.example":    false,
		"":                false,
	} {
		if err := ValidateZone(zone); (err == nil) != ok {
			t.Errorf("ValidateZone(%q) = %v, want ok=%t", zone, err, ok)
		}
	}
}

func TestServingZonePrefersTheMostSpecificZone(t *testing.T) {
	zones := []string{"example.com", "lab.example.com", "other.dev"}
	for name, want := range map[string]string{
		"example.com":         "example.com",
		"www.example.com":     "example.com",
		"lab.example.com":     "lab.example.com", // apex of the nested zone
		"app.lab.example.com": "lab.example.com",
		"APP.Other.Dev.":      "other.dev",
		"a.b.other.dev":       "", // a wildcard covers one label
		"example.org":         "",
		"":                    "",
	} {
		got, ok := ServingZone(zones, name)
		if got != want || ok != (want != "") {
			t.Errorf("ServingZone(%q) = %q, %t; want %q", name, got, ok, want)
		}
	}
}

func TestStateOf(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	later := now.Add(60 * 24 * time.Hour)
	for _, c := range []struct {
		status Status
		want   ZoneState
	}{
		{Status{}, ZonePending},
		{Status{Waiting: true}, ZoneQueued},
		{Status{Attempting: true}, ZoneIssuing},
		{Status{LastOutcome: "failed"}, ZoneFailed},
		{Status{Valid: true, NotAfter: later}, ZoneActive},
		{Status{Valid: true, NotAfter: later, Attempting: true}, ZoneRenewing},
		{Status{Valid: true, NotAfter: later, LastOutcome: "failed"}, ZoneRenewalFailed},
		{Status{Valid: true, NotAfter: now.Add(3 * 24 * time.Hour), LastOutcome: "failed"}, ZoneExpiring},
	} {
		if got := StateOf(c.status, now); got != c.want {
			t.Errorf("StateOf(%+v) = %s, want %s", c.status, got, c.want)
		}
	}
}

// zoneIssuer issues per-zone outcomes and records ordering: how many
// Obtain calls overlap, and whether Recover ran before any of them.
type zoneIssuer struct {
	mu           sync.Mutex
	outcomes     map[string]func(context.Context) (StoredGeneration, error)
	obtained     []string
	active       atomic.Int32
	maxActive    atomic.Int32
	recovered    atomic.Int32
	obtainBefore atomic.Bool // an Obtain ran before Recover
}

func (z *zoneIssuer) Obtain(ctx context.Context, names []string, _ crypto.Signer) (StoredGeneration, error) {
	if z.recovered.Load() == 0 {
		z.obtainBefore.Store(true)
	}
	n := z.active.Add(1)
	defer z.active.Add(-1)
	for {
		m := z.maxActive.Load()
		if n <= m || z.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	zone := names[1]
	z.mu.Lock()
	z.obtained = append(z.obtained, zone)
	outcome := z.outcomes[zone]
	z.mu.Unlock()
	if outcome == nil {
		return StoredGeneration{}, errors.New("no outcome for " + zone)
	}
	return outcome(ctx)
}

func (z *zoneIssuer) Recover(context.Context) (int, error) {
	z.recovered.Add(1)
	return 2, nil
}

func (z *zoneIssuer) count(zone string) int {
	z.mu.Lock()
	defer z.mu.Unlock()
	n := 0
	for _, o := range z.obtained {
		if o == zone {
			n++
		}
	}
	return n
}

type zoneFixture struct {
	now   time.Time
	gens  map[string]StoredGeneration
	store *FileStore
}

func newZoneFixture(t *testing.T, zones ...string) zoneFixture {
	t.Helper()
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	f := zoneFixture{now: now, gens: map[string]StoredGeneration{}, store: testStore(t)}
	for _, zone := range zones {
		names := ZoneNames(zone)
		f.gens[zone], _ = testGeneration(t, names[0], now.Add(-time.Hour), now.Add(90*24*time.Hour), names[1:]...)
	}
	return f
}

func (f zoneFixture) config(t *testing.T, attempt time.Duration, extra ...StoredGeneration) ManagerConfig {
	gens := extra
	for _, g := range f.gens {
		gens = append(gens, g)
	}
	return ManagerConfig{
		RootCAs:        testRoots(t, gens...),
		Now:            func() time.Time { return f.now },
		Jitter:         func(d time.Duration) time.Duration { return d },
		AttemptTimeout: attempt,
		BackoffBase:    time.Hour,
		BackoffMax:     time.Hour,
	}
}

func (f zoneFixture) issued(zone string) func(context.Context) (StoredGeneration, error) {
	return func(context.Context) (StoredGeneration, error) { return f.gens[zone], nil }
}

func runZones(t *testing.T, z *Zones) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); z.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("zones did not stop")
		}
	})
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func served(z *Zones, name string) bool {
	_, err := z.GetCertificate(&tls.ClientHelloInfo{ServerName: name})
	return err == nil
}

// A zone whose issuance fails never holds back another zone, and every
// issuance runs alone, after the one recovery.
func TestZonesIsolateFailuresAndTakeTurns(t *testing.T) {
	f := newZoneFixture(t, "good.dev", "also.dev", "bad.dev")
	issuer := &zoneIssuer{outcomes: map[string]func(context.Context) (StoredGeneration, error){
		"good.dev": f.issued("good.dev"),
		"also.dev": func(ctx context.Context) (StoredGeneration, error) {
			time.Sleep(20 * time.Millisecond) // overlap would show here
			return f.gens["also.dev"], nil
		},
		"bad.dev": func(context.Context) (StoredGeneration, error) {
			return StoredGeneration{}, ErrDNS
		},
	}}
	z, err := NewZones(f.config(t, time.Minute), f.store, issuer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Set(context.Background(), []string{"good.dev", "also.dev", "bad.dev"}); err != nil {
		t.Fatal(err)
	}
	runZones(t, z)
	eventually(t, "every zone to finish an attempt", func() bool {
		for _, s := range z.Status() {
			if s.LastOutcome == "" || s.Attempting {
				return false
			}
		}
		return true
	})
	if issuer.count("good.dev") != 1 || issuer.count("also.dev") != 1 || issuer.count("bad.dev") != 1 {
		t.Fatal("each zone must attempt exactly once")
	}
	for _, name := range []string{"good.dev", "www.good.dev", "also.dev", "x.also.dev"} {
		if !served(z, name) {
			t.Errorf("%s not served although its zone issued", name)
		}
	}
	if served(z, "bad.dev") || served(z, "www.bad.dev") {
		t.Error("a zone without a certificate must fail closed")
	}
	if issuer.maxActive.Load() != 1 {
		t.Errorf("issuances overlapped: %d at once", issuer.maxActive.Load())
	}
	if issuer.recovered.Load() != 1 || issuer.obtainBefore.Load() {
		t.Errorf("recovery must run once before any issuance (ran %d, obtain first=%t)", issuer.recovered.Load(), issuer.obtainBefore.Load())
	}
	if z.PendingIntents() != 2 {
		t.Errorf("pending intents = %d, want the recovery's 2", z.PendingIntents())
	}
	states := map[string]ZoneState{}
	for _, s := range z.Status() {
		states[s.Zone] = s.State
	}
	if states["good.dev"] != ZoneActive || states["also.dev"] != ZoneActive || states["bad.dev"] != ZoneFailed {
		t.Errorf("states = %v", states)
	}
}

// Waiting for another zone's issuance never counts against a zone's own
// attempt timeout.
func TestZoneWaitingForItsTurnDoesNotTimeOut(t *testing.T) {
	f := newZoneFixture(t, "slow.dev", "quick.dev")
	slowStarted := make(chan struct{})
	issuer := &zoneIssuer{outcomes: map[string]func(context.Context) (StoredGeneration, error){
		"slow.dev": func(ctx context.Context) (StoredGeneration, error) {
			close(slowStarted)
			time.Sleep(200 * time.Millisecond)
			return f.gens["slow.dev"], nil
		},
		// 200 ms waiting plus 100 ms issuing would pass the 250 ms timeout
		// if the wait counted.
		"quick.dev": func(context.Context) (StoredGeneration, error) {
			time.Sleep(100 * time.Millisecond)
			return f.gens["quick.dev"], nil
		},
	}}
	z, err := NewZones(f.config(t, 250*time.Millisecond), f.store, issuer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Set(context.Background(), []string{"slow.dev"}); err != nil {
		t.Fatal(err)
	}
	runZones(t, z)
	<-slowStarted
	// Added while slow.dev holds the turn.
	if err := z.Set(context.Background(), []string{"slow.dev", "quick.dev"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "quick.dev to be served", func() bool { return served(z, "quick.dev") })
	for _, s := range z.Status() {
		if s.LastOutcome == "failed" {
			t.Errorf("%s failed: %s", s.Zone, s.LastError)
		}
	}
}

// The certificate in service before zones keeps answering for its names
// until the zone has its own, and zone certificates never overwrite it.
func TestZonesFallBackToTheCertificateInService(t *testing.T) {
	f := newZoneFixture(t, "one.dev", "two.dev")
	legacy, _ := testGeneration(t, "*.one.dev", f.now.Add(-time.Hour), f.now.Add(60*24*time.Hour), "one.dev", "two.dev", "*.two.dev")
	if err := f.store.Save(context.Background(), "*.one.dev", legacy); err != nil {
		t.Fatal(err)
	}
	cfg := f.config(t, time.Minute, legacy)
	legacyCfg := cfg
	legacyCfg.Host, legacyCfg.AltNames = "*.one.dev", []string{"one.dev", "two.dev", "*.two.dev"}
	old, err := NewManager(legacyCfg, f.store, &stubIssuer{})
	if err != nil {
		t.Fatal(err)
	}
	old.LoadStored(context.Background())

	release := make(chan struct{})
	issuer := &zoneIssuer{outcomes: map[string]func(context.Context) (StoredGeneration, error){
		"one.dev": f.issued("one.dev"),
		"two.dev": func(ctx context.Context) (StoredGeneration, error) {
			select {
			case <-release:
				return f.gens["two.dev"], nil
			case <-ctx.Done():
				return StoredGeneration{}, ctx.Err()
			}
		},
	}}
	z, err := NewZones(cfg, f.store, issuer, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Set(context.Background(), []string{"one.dev"}); err != nil {
		t.Fatal(err)
	}
	legacySerial := func(name string) bool {
		cert, err := z.GetCertificate(&tls.ClientHelloInfo{ServerName: name})
		// The old certificate is the only one carrying four names.
		return err == nil && cert.Leaf.Subject.CommonName == "*.one.dev" && len(cert.Leaf.DNSNames) == 4
	}
	if !legacySerial("two.dev") || !legacySerial("www.one.dev") {
		t.Fatal("before zones issue, the certificate in service must answer")
	}
	if served(z, "three.dev") {
		t.Fatal("a name outside every zone and the old certificate must fail closed")
	}
	runZones(t, z)
	eventually(t, "one.dev to get its own certificate", func() bool {
		cert, err := z.GetCertificate(&tls.ClientHelloInfo{ServerName: "one.dev"})
		return err == nil && len(cert.Leaf.DNSNames) == 2
	})
	// Added only now: a blocked issuance holds the turn, so it must not
	// start before one.dev's.
	if err := z.Set(context.Background(), []string{"one.dev", "two.dev"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "two.dev to start issuing", func() bool { return issuer.count("two.dev") == 1 })
	if !legacySerial("two.dev") {
		t.Fatal("two.dev must keep the old certificate while its own is issued")
	}
	close(release)
	eventually(t, "two.dev to get its own certificate", func() bool {
		cert, err := z.GetCertificate(&tls.ClientHelloInfo{ServerName: "two.dev"})
		return err == nil && len(cert.Leaf.DNSNames) == 2
	})
	if gen, ok, err := f.store.Load(context.Background(), "*.one.dev"); err != nil || !ok || string(gen.CertPEM) != string(legacy.CertPEM) {
		t.Fatal("a zone certificate overwrote the certificate in service before zones")
	}
}

// Zones are added and removed while running: a removed zone stops being
// served and stops renewing.
func TestZonesSetWhileRunning(t *testing.T) {
	f := newZoneFixture(t, "keep.dev", "drop.dev", "new.dev")
	issuer := &zoneIssuer{outcomes: map[string]func(context.Context) (StoredGeneration, error){
		"keep.dev": f.issued("keep.dev"), "drop.dev": f.issued("drop.dev"), "new.dev": f.issued("new.dev"),
	}}
	z, err := NewZones(f.config(t, time.Minute), f.store, issuer, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := z.Set(ctx, []string{"keep.dev", "drop.dev"}); err != nil {
		t.Fatal(err)
	}
	runZones(t, z)
	eventually(t, "both zones served", func() bool { return served(z, "keep.dev") && served(z, "drop.dev") })
	if err := z.Set(ctx, []string{"keep.dev", "new.dev"}); err != nil {
		t.Fatal(err)
	}
	if served(z, "drop.dev") {
		t.Fatal("a removed zone is still served")
	}
	eventually(t, "the new zone served", func() bool { return served(z, "new.dev") })
	if issuer.count("keep.dev") != 1 {
		t.Fatalf("an unchanged zone was reissued: %d", issuer.count("keep.dev"))
	}
	// A bad set changes nothing.
	if err := z.Set(ctx, []string{"keep.dev", "*.evil.dev"}); err == nil {
		t.Fatal("wildcard zone accepted")
	}
	if !served(z, "new.dev") {
		t.Fatal("a refused set removed a zone")
	}
	// A restarted zone reloads its own stored certificate without issuing.
	if err := z.Set(ctx, []string{"keep.dev"}); err != nil {
		t.Fatal(err)
	}
	if err := z.Set(ctx, []string{"keep.dev", "new.dev"}); err != nil {
		t.Fatal(err)
	}
	if !served(z, "new.dev") || issuer.count("new.dev") != 1 {
		t.Fatalf("re-added zone: served=%t issued=%d", served(z, "new.dev"), issuer.count("new.dev"))
	}
}

// An operator who fixed a record renews at once instead of waiting out the
// backoff, but cannot hammer the CA.
func TestZoneRenewNowSkipsTheBackoffWithinLimits(t *testing.T) {
	f := newZoneFixture(t, "fixed.dev")
	var mu sync.Mutex
	fixed := false
	issuer := &zoneIssuer{outcomes: map[string]func(context.Context) (StoredGeneration, error){
		"fixed.dev": func(context.Context) (StoredGeneration, error) {
			mu.Lock()
			defer mu.Unlock()
			if !fixed {
				return StoredGeneration{}, ErrDNS
			}
			return f.gens["fixed.dev"], nil
		},
	}}
	clock := f.now
	var clockMu sync.Mutex
	cfg := f.config(t, time.Minute)
	cfg.Now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	z, err := NewZones(cfg, f.store, issuer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Set(context.Background(), []string{"fixed.dev"}); err != nil {
		t.Fatal(err)
	}
	if err := z.Renew("other.dev"); !errors.Is(err, ErrUnknownZone) {
		t.Fatalf("unknown zone: %v", err)
	}
	runZones(t, z)
	eventually(t, "the first attempt to fail", func() bool { return z.Status()[0].LastOutcome == "failed" && !z.Status()[0].Attempting })
	// The backoff is an hour; the record is fixed now.
	if err := z.Renew("fixed.dev"); !errors.Is(err, ErrRenewTooSoon) {
		t.Fatalf("renew right after an attempt: %v", err)
	}
	clockMu.Lock()
	clock = clock.Add(MinManualInterval)
	clockMu.Unlock()
	mu.Lock()
	fixed = true
	mu.Unlock()
	if err := z.Renew("fixed.dev"); err != nil {
		t.Fatalf("renew after the interval: %v", err)
	}
	eventually(t, "the zone to be served after renewing", func() bool { return served(z, "fixed.dev") })
	if issuer.count("fixed.dev") != 2 {
		t.Fatalf("attempts = %d, want the failed one and the renewal", issuer.count("fixed.dev"))
	}
}

func TestRenewNowRespectsTheCARateLimit(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	m := testManager(t, testStore(t), &stubIssuer{}, now)
	m.recordFailure(now.Add(-2*MinManualInterval), &RateLimitedError{RetryAfter: time.Hour, Err: errors.New("too many")})
	if err := m.RenewNow(); !errors.Is(err, ErrRenewTooSoon) {
		t.Fatalf("renew during the CA's wait: %v", err)
	}
}
