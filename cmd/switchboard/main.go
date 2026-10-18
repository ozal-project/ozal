package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/octaviosantacruz/switchboard/internal/admin"
	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/originauth"
	"github.com/octaviosantacruz/switchboard/internal/routemanager"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

const healthCheckInterval = 10 * time.Second

// subcommands are the one-shot commands run as `switchboard <name> ...`. Each
// gets a context that SIGINT/SIGTERM cancel, so none can outlive a stop signal.
func subcommands() map[string]func(context.Context, []string, io.Writer) error {
	return map[string]func(context.Context, []string, io.Writer) error{
		"tunnel":       runTunnel,
		"tunnel-spike": runTunnelSpike,
		"connector":    runConnector,
		"migrate":      runMigrate,
	}
}

func main() {
	if len(os.Args) > 1 {
		if run, ok := subcommands()[os.Args[1]]; ok {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			err := run(ctx, os.Args[2:], os.Stdout)
			stop() // log.Fatalf exits without running deferred calls
			if err != nil {
				log.Fatalf("%s: %v", os.Args[1], err)
			}
			return
		}
	}

	configPath := flag.String("config", "config.json", "path to routing config")
	dbPath := flag.String("db", "", "path to the migrated SQLite state database (empty keeps sessions in memory)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Decisions, logins and control-plane changes are published as JSON
	// records, one event per line.
	// The broker keeps recent events for the admin activity page.
	broker := events.NewBroker()
	// One structured stream: events, and through the default logger also the
	// proxy's operational records and every log.Printf in the process.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	sink := events.Multi{events.SlogSink{Logger: logger}, broker}
	managerOpts := []routemanager.Option{routemanager.WithEventSink(sink)}
	if *dbPath != "" {
		// Sessions survive a restart through the migrated state database.
		// Migrations stay an explicit operator step: a stale schema stops
		// startup instead of being applied silently.
		db, err := openMigratedDB(ctx, *dbPath)
		if err != nil {
			log.Fatalf("state database: %v", err)
		}
		defer db.Close()
		managerOpts = append(managerOpts, routemanager.WithSessionBackend(func(ttl time.Duration) auth.SessionBackend {
			return sqlite.NewSessions(db, ttl)
		}))
		log.Printf("sessions: persistent store at %s", *dbPath)
	}
	manager, err := routemanager.New(ctx, *configPath, healthCheckInterval, managerOpts...)
	if err != nil {
		log.Fatalf("routes: %v", err)
	}
	cfg := manager.Config()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				event := events.Event{Type: events.ConfigReloaded, Actor: "signal:SIGHUP", Time: time.Now()}
				if err := manager.Reload(); err != nil {
					event.Error = err.Error()
				}
				sink.Emit(event)
			}
		}
	}()

	log.Printf("routes:")
	for _, r := range cfg.Routes {
		log.Printf("  %s -> %s", r.Host, r.Destination())
	}

	adminServer, err := startAdmin(&cfg, manager, sink, broker)
	if err != nil {
		log.Fatalf("admin listener: %v", err)
	}
	servers := []*http.Server{adminServer}
	if cfg.TLS.Enabled {
		tlsServers, err := startTLS(&cfg, manager, sink)
		if err != nil {
			log.Fatalf("public listener: %v", err)
		}
		servers = append(servers, tlsServers...)
	} else {
		plainServer, err := startPlain(&cfg, manager)
		if err != nil {
			log.Fatalf("public listener: %v", err)
		}
		servers = append(servers, plainServer)
	}

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range servers {
		if err := s.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown %s: %v", s.Addr, err)
		}
	}
}

// Timeouts guard the listener itself against slow-client attacks
// (Slowloris et al.) without limiting how long a proxied request/response
// body can take — a route might legitimately stream a large download or
// hold open a long-lived connection (WebSocket/SSE support is planned),
// so ReadTimeout/WriteTimeout are deliberately left unset rather than
// capping every request's total duration.
const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 120 * time.Second
)

func startAdmin(cfg *config.Config, manager *routemanager.Manager, sink events.Sink, broker *events.Broker) (*http.Server, error) {
	listener, err := net.Listen("tcp", cfg.Admin.Listen)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Addr:              cfg.Admin.Listen,
		Handler:           admin.New(manager, sink, admin.WithEvents(broker)),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		log.Printf("switchboard admin listening on %s", cfg.Admin.Listen)
		if cfg.Admin.Token == "" {
			log.Printf("admin mutations disabled: set admin.token in config")
		}
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("admin listener %s: %v", cfg.Admin.Listen, err)
		}
	}()
	return server, nil
}

// startPlain binds synchronously so an occupied port fails startup with a
// clear error instead of a log.Fatalf in a goroutine that skips shutdown.
func startPlain(cfg *config.Config, handler http.Handler) (*http.Server, error) {
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		log.Printf("switchboard listening on %s (plain HTTP)", cfg.Listen)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("public listener %s: %v", cfg.Listen, err)
		}
	}()
	return server, nil
}

// startAutocertTLS is the HTTP-01 path: HTTPS on :443 with autocert, plus a
// plain HTTP server on :80 that answers challenges and redirects everything
// else. Both ports bind synchronously: an occupied port must fail startup,
// not a goroutine.
func startTLS(cfg *config.Config, handler http.Handler, sink events.Sink) ([]*http.Server, error) {
	manager := &autocert.Manager{
		Cache:      autocert.DirCache(cfg.TLS.CacheDir),
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.TLS.Domains...),
		Email:      cfg.TLS.Email,
	}

	tlsConfig := manager.TLSConfig()
	if cfg.TLS.OriginClientCAFile != "" {
		// Fail closed at startup: an unreadable or empty bundle must stop the
		// listener, never leave it open.
		bundle, err := os.ReadFile(cfg.TLS.OriginClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read tls.origin_client_ca_file: %w", err)
		}
		roots, err := originauth.LoadPool(bundle)
		if err != nil {
			return nil, fmt.Errorf("tls.origin_client_ca_file: %w", err)
		}
		tlsConfig = originauth.ServerTLS(tlsConfig, roots, sink)
		handler = originauth.RequireVerified(handler)
	}

	httpsListener, err := net.Listen("tcp", ":443")
	if err != nil {
		return nil, err
	}
	challengeListener, err := net.Listen("tcp", ":80")
	if err != nil {
		httpsListener.Close()
		return nil, err
	}

	httpsServer := &http.Server{
		Addr:              ":443",
		Handler:           handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		log.Printf("switchboard listening on :443 (HTTPS, ACME domains: %v)", cfg.TLS.Domains)
		if err := httpsServer.ServeTLS(httpsListener, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("public listener :443: %v", err)
		}
	}()

	challengeServer := &http.Server{
		Addr: ":80",
		Handler: manager.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := "https://" + r.Host + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		})),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		log.Printf("switchboard listening on :80 (ACME challenges + HTTPS redirect)")
		if err := challengeServer.Serve(challengeListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("public listener :80: %v", err)
		}
	}()

	return []*http.Server{httpsServer, challengeServer}, nil
}
