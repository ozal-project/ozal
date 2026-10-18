package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/tunnel"
)

// configuredTunnel is the composition-root adapter for ordinary route snapshots.
// It shares the explicitly migrated state database with persistent sessions.
type configuredTunnel struct {
	edge     *tunnel.Edge
	registry *tunnel.Registry
	address  string
	cancel   context.CancelFunc
	done     chan struct{}
	err      error // published by closing done
	once     sync.Once
}

func startIntegratedTunnel(ctx context.Context, cfg *config.Tunnel, db *sql.DB, sink events.Sink) (*configuredTunnel, error) {
	if cfg == nil {
		return nil, errors.New("tunnel configuration is required")
	}
	if db == nil {
		return nil, errors.New("configured tunnel requires -db pointing at migrated state")
	}
	network, err := config.ParseTunnelNetwork(cfg.Network)
	if err != nil {
		return nil, err
	}
	if err := config.ValidateTunnelAddress(cfg.Listen, true, network); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cert, roots, err := tunnel.LoadIdentity(cfg.CertFile, cfg.KeyFile, cfg.CAFile)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := tunnel.ServerTLSRegistry(cert, roots)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	registry := tunnel.NewRegistry(db)
	edge := tunnel.NewEdge(sink)
	edge.UseAuthorizer(registry)
	running := &configuredTunnel{edge: edge, registry: registry, address: listener.Addr().String(), cancel: cancel, done: make(chan struct{})}
	go func() { defer close(running.done); running.err = edge.Serve(runCtx, listener, tlsConfig) }()
	return running, nil
}

func (t *configuredTunnel) TransportFor(ctx context.Context, name string) (http.RoundTripper, error) {
	if _, err := t.registry.Current(ctx, name); err != nil {
		return nil, fmt.Errorf("connector %s is not actively enrolled: %w", name, err)
	}
	return t.edge.TransportFor(name), nil
}
func (t *configuredTunnel) ConnectorConnected(name string) bool {
	return t.edge.ConnectorConnected(name)
}
func (t *configuredTunnel) Close() { t.once.Do(func() { t.cancel(); <-t.done }) }
