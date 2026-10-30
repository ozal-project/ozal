package certs

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Zones serves one certificate per zone (EDGE-008). Each zone gets its own
// Manager — own storage, backoff and renewal — and GetCertificate picks the
// zone that serves the requested name. The zones share one issuer (one ACME
// account and DNS provider), which takes one call at a time, so managers
// take turns: the wait is outside each attempt's timeout, and DNS cleanup
// recovery runs once, before any zone issues, so it can never remove a
// challenge record another zone has out.
type Zones struct {
	base     ManagerConfig
	store    Store
	issuer   Issuer
	fallback certificateSource
	turn     chan struct{}

	mu       sync.Mutex
	zones    map[string]*zoneRun
	ctx      context.Context // set by Run; nil until then
	recovery int
}

// certificateSource is what Zones needs from the certificate in service
// before zones: a read-only Manager loaded from its old storage.
type certificateSource interface {
	GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
}

type zoneRun struct {
	manager *Manager
	cancel  context.CancelFunc
	done    chan struct{}
}

// ZoneStatus is one zone's certificate state for the console.
type ZoneStatus struct {
	Zone  string
	State ZoneState
	Status
}

// NewZones builds the per-zone set. base supplies timing, roots, logger and
// sink for every zone; its Host and AltNames are ignored. fallback, when not
// nil, keeps serving the names of the certificate in service before zones
// while a zone has no valid certificate of its own.
func NewZones(base ManagerConfig, store Store, issuer Issuer, fallback certificateSource) (*Zones, error) {
	if store == nil || issuer == nil {
		return nil, errors.New("certs: zones require a store and an issuer")
	}
	base.Host, base.AltNames = "", nil
	return &Zones{
		base: base, store: store, issuer: issuer, fallback: fallback,
		turn:  make(chan struct{}, 1),
		zones: map[string]*zoneRun{},
	}, nil
}

// Set makes zones the served set: new zones load their stored certificate
// at once (and start issuing when Run is active), removed zones stop. It
// validates every zone before changing anything.
func (z *Zones) Set(ctx context.Context, zones []string) error {
	want := map[string]bool{}
	for _, zone := range zones {
		if err := ValidateZone(zone); err != nil {
			return err
		}
		if want[zone] {
			return fmt.Errorf("certs: zone %q is repeated", zone)
		}
		want[zone] = true
	}
	z.mu.Lock()
	var stopping []*zoneRun
	for zone, run := range z.zones {
		if !want[zone] {
			stopping = append(stopping, run)
			delete(z.zones, zone)
		}
	}
	var err error
	for zone := range want {
		if _, ok := z.zones[zone]; ok {
			continue
		}
		run, buildErr := z.build(zone)
		if buildErr != nil {
			err = errors.Join(err, buildErr)
			continue
		}
		run.manager.LoadStored(ctx)
		z.zones[zone] = run
		if z.ctx != nil {
			z.start(run)
		}
	}
	z.mu.Unlock()
	for _, run := range stopping {
		run.stop()
	}
	return err
}

func (z *Zones) build(zone string) (*zoneRun, error) {
	cfg := z.base
	names := ZoneNames(zone)
	cfg.Host, cfg.AltNames = names[0], names[1:]
	cfg.Turn = z.take
	cfg.NoRecovery = true
	manager, err := NewManager(cfg, zoneStore{z.store, ZoneStorageKey(zone)}, z.issuer)
	if err != nil {
		return nil, err
	}
	return &zoneRun{manager: manager}, nil
}

// start runs a zone's manager under Run's context. z.mu is held.
func (z *Zones) start(run *zoneRun) {
	ctx, cancel := context.WithCancel(z.ctx)
	run.cancel, run.done = cancel, make(chan struct{})
	go func() {
		defer close(run.done)
		run.manager.Run(ctx)
	}()
}

func (r *zoneRun) stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
}

// take is every manager's Turn: one issuance at a time across zones.
func (z *Zones) take(ctx context.Context) (func(), error) {
	select {
	case z.turn <- struct{}{}:
		return func() { <-z.turn }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Run recovers DNS cleanup once, starts every zone and owns them until ctx
// is cancelled; it returns after every zone has stopped.
func (z *Zones) Run(ctx context.Context) {
	if release, err := z.take(ctx); err == nil {
		pending, err := z.issuer.Recover(ctx)
		release()
		if err != nil {
			z.base.logger().Warn("dns cleanup recovery did not finish", "error", err)
		}
		z.mu.Lock()
		z.recovery = pending
		z.mu.Unlock()
	}
	z.mu.Lock()
	if ctx.Err() != nil {
		z.mu.Unlock()
		return
	}
	z.ctx = ctx
	for _, run := range z.zones {
		z.start(run)
	}
	z.mu.Unlock()

	<-ctx.Done()
	z.mu.Lock()
	runs := make([]*zoneRun, 0, len(z.zones))
	for _, run := range z.zones {
		runs = append(runs, run)
	}
	z.mu.Unlock()
	for _, run := range runs {
		run.stop()
	}
}

// GetCertificate serves the certificate of the zone that covers the name.
// While that zone has no valid certificate yet, the certificate in service
// before zones answers for the names it carries; anything else fails
// closed.
func (z *Zones) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if hello == nil {
		return nil, errors.New("certs: no client hello")
	}
	z.mu.Lock()
	zone, ok := ServingZone(z.names(), hello.ServerName)
	var manager *Manager
	if ok {
		manager = z.zones[zone].manager
	}
	z.mu.Unlock()
	var zoneErr error
	if manager != nil {
		cert, err := manager.GetCertificate(hello)
		if err == nil {
			return cert, nil
		}
		zoneErr = err
	}
	if z.fallback != nil {
		if cert, err := z.fallback.GetCertificate(hello); err == nil {
			return cert, nil
		}
	}
	if zoneErr != nil {
		return nil, zoneErr
	}
	return nil, fmt.Errorf("certs: no zone serves %q", hello.ServerName)
}

// Status lists every zone, sorted by name.
func (z *Zones) Status() []ZoneStatus {
	z.mu.Lock()
	names := z.names()
	runs := make([]*zoneRun, len(names))
	for i, zone := range names {
		runs[i] = z.zones[zone]
	}
	z.mu.Unlock()
	out := make([]ZoneStatus, len(names))
	for i, zone := range names {
		status := runs[i].manager.Status()
		out[i] = ZoneStatus{Zone: zone, State: StateOf(status, z.base.nowOrDefault()), Status: status}
	}
	return out
}

// ErrUnknownZone: the zone is not served.
var ErrUnknownZone = errors.New("certs: no such zone")

// Renew asks one zone for an issuance attempt now (Manager.RenewNow).
func (z *Zones) Renew(zone string) error {
	z.mu.Lock()
	run, ok := z.zones[zone]
	z.mu.Unlock()
	if !ok {
		return ErrUnknownZone
	}
	return run.manager.RenewNow()
}

// PendingIntents is the DNS cleanup left after the startup recovery.
func (z *Zones) PendingIntents() int {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.recovery
}

// names returns the zones, sorted. z.mu is held.
func (z *Zones) names() []string {
	names := make([]string, 0, len(z.zones))
	for zone := range z.zones {
		names = append(names, zone)
	}
	slices.Sort(names)
	return names
}

// zoneStore stores a zone's generations under its own key, whatever host
// the manager passes.
type zoneStore struct {
	Store
	key string
}

func (s zoneStore) Load(ctx context.Context, _ string) (StoredGeneration, bool, error) {
	return s.Store.Load(ctx, s.key)
}

func (s zoneStore) Save(ctx context.Context, _ string, gen StoredGeneration) error {
	return s.Store.Save(ctx, s.key, gen)
}

func (c ManagerConfig) logger() *slog.Logger {
	if c.Logger == nil {
		return slog.Default()
	}
	return c.Logger
}

func (c ManagerConfig) nowOrDefault() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}
