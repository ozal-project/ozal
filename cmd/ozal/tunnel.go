package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/proxy"
	"github.com/ozal-project/ozal/internal/tunnel"
)

// runTunnel is the persistent, registry-backed slice of TUN-002.
// Certificates are supplied by the operator and never generated on startup.
// HTTP listeners/origins remain local; mTLS may opt into one private network.
func runTunnel(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "ca" || args[0] == "identity") {
		return runTunnelAuthority(ctx, args, out)
	}
	if len(args) > 0 && args[0] == "renew" {
		return runTunnelRenew(ctx, args[1:], out)
	}
	if len(args) > 0 && args[0] == "renew-agent" {
		return runTunnelRenewAgent(ctx, args[1:], out)
	}
	if len(args) == 0 || (args[0] != "edge" && args[0] != "connect") {
		return errors.New("usage: ozal tunnel edge|connect [flags]")
	}
	mode := args[0]
	fs := flag.NewFlagSet("tunnel "+mode, flag.ContinueOnError)
	fs.SetOutput(out)
	certPath := fs.String("cert", "", "persistent leaf certificate PEM path")
	keyPath := fs.String("key", "", "persistent private key PEM path")
	caPath := fs.String("ca", "", "trusted CA certificate PEM path")
	host := fs.String("host", "", "one allowed request host (required)")
	network := fs.String("tunnel-network", "", "optional private CIDR allowed for mTLS transport; HTTP remains loopback-only")
	var dbPath, name, listen, ingress, edgeAddr, serverName, root, upstream string
	var extraRoutes []string
	if mode == "edge" {
		fs.StringVar(&dbPath, "db", defaultDBPath, "migrated SQLite connector registry")
		fs.StringVar(&name, "connector", "", "enrolled connector name for this host (required)")
		fs.StringVar(&listen, "listen", "127.0.0.1:0", "HTTP loopback listener")
		fs.StringVar(&ingress, "tunnel-listen", "127.0.0.1:0", "mTLS loopback listener")
	} else {
		fs.StringVar(&edgeAddr, "edge", "", "edge mTLS loopback address (required)")
		fs.StringVar(&serverName, "server-name", "", "expected edge certificate DNS name (required)")
		fs.StringVar(&root, "root", "", "fixed absolute static site directory")
		fs.StringVar(&upstream, "upstream", "", "fixed HTTP loopback origin")
		fs.Func("route", "additional fixed route HOST=root:DIR or HOST=upstream:URL (repeatable)", func(v string) error {
			extraRoutes = append(extraRoutes, v)
			return nil
		})
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *certPath == "" || *keyPath == "" || *caPath == "" {
		return errors.New("-cert, -key and -ca are required; identities are never generated at startup")
	}
	normalized, err := config.NormalizeHost(*host)
	if err != nil {
		return errors.New("-host must be a valid route host")
	}
	privateNetwork, err := parseTunnelNetwork(*network)
	if err != nil {
		return err
	}
	if mode == "edge" {
		if name == "" {
			return errors.New("-connector is required")
		}
		if err := validateLabAddress(listen, true); err != nil {
			return err
		}
		if err := validateTunnelAddress(ingress, true, privateNetwork); err != nil {
			return err
		}
	} else {
		if err := validateTunnelAddress(edgeAddr, false, privateNetwork); err != nil {
			return err
		}
		if serverName == "" {
			return errors.New("-server-name is required")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cert, roots, err := tunnel.LoadIdentity(*certPath, *keyPath, *caPath)
	if err != nil {
		return err
	}
	if mode == "connect" {
		routes, err := connectorRoutes(normalized, root, upstream, extraRoutes)
		if err != nil {
			return err
		}
		cfg, err := tunnel.ClientTLS(cert, roots, serverName)
		if err != nil {
			return err
		}
		// This slice serves anonymous routes. The connector's trust stage
		// strips identity/forwarding headers rather than trusting edge claims.
		handler, err := proxy.New(routes, nil, nil, proxy.WithAuthenticatedTunnelTrust())
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Connecting with persistent identity; automatic reconnect enabled; anonymous route only.")
		return tunnel.ConnectLoop(ctx, edgeAddr, cfg, handler, tunnel.LoopOptions{})
	}
	cfg, err := tunnel.ServerTLSRegistry(cert, roots)
	if err != nil {
		return err
	}
	db, err := openMigratedDB(ctx, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	registry := tunnel.NewRegistry(db)
	records, err := registry.List(ctx)
	if err != nil {
		return err
	}
	active := false
	for _, record := range records {
		if record.Name == name && record.RevokedAt.IsZero() {
			active = true
			break
		}
	}
	if !active {
		return errors.New("-connector must name an active enrolment; use `ozal connector enroll` first")
	}
	sink := events.SlogSink{Logger: slog.New(slog.NewJSONHandler(out, nil))}
	edge := tunnel.NewEdge(sink)
	edge.UseAuthorizer(registry) // No registry-less fallback in this command.
	handler, closeTransport, err := edge.HandlerFor(normalized, name)
	if err != nil {
		return err
	}
	defer closeTransport()
	return serveTunnelEdge(ctx, edge, cfg, handler, listen, ingress, normalized, out)
}

// serveTunnelEdge owns both listeners and joins both workers on every exit.
func serveTunnelEdge(ctx context.Context, edge *tunnel.Edge, cfg *tls.Config, handler http.Handler, listen, ingress, host string, out io.Writer) error {
	httpListener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	defer httpListener.Close()
	tunnelListener, err := net.Listen("tcp", ingress)
	if err != nil {
		return err
	}
	defer tunnelListener.Close()
	fmt.Fprintf(out, "HTTP http://%s (Host: %s)\nTUNNEL %s\n", httpListener.Addr(), host, tunnelListener.Addr())
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, MaxHeaderBytes: 64 << 10,
		BaseContext: func(net.Listener) context.Context { return runCtx },
	}
	defer server.Close()
	done := make(chan error, 2)
	go func() { done <- edge.Serve(runCtx, tunnelListener, cfg) }()
	go func() {
		err := server.Serve(httpListener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	var result error
	finished := 0
	select {
	case <-ctx.Done():
	case result = <-done:
		finished++
	}
	cancel()
	_ = server.Close()
	for finished < 2 {
		workerErr := <-done
		if result == nil {
			result = workerErr
		}
		finished++
	}
	return result
}
