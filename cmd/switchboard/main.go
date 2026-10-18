package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"reflect"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/octaviosantacruz/switchboard/internal/admin"
	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/certs"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/notify"
	"github.com/octaviosantacruz/switchboard/internal/notify/telegram"
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
		"notify":       runNotify,
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
	// The config is loaded here as well to decide whether notifications are
	// on; the route manager loads and validates it again for routing.
	loaded, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	alertSink, err := notificationSink(ctx, loaded, logger)
	if err != nil {
		log.Fatalf("notifications: %v", err)
	}
	sink := events.Multi{events.SlogSink{Logger: logger}, broker}
	if alertSink != nil {
		sink = append(sink, alertSink)
	}
	managerOpts := []routemanager.Option{routemanager.WithEventSink(sink)}
	var configuredEdge *configuredTunnel
	if loaded.Tunnel != nil && *dbPath == "" {
		log.Fatal("tunnel configuration requires -db pointing at migrated state")
	}
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
		if loaded.Tunnel != nil {
			configuredEdge, err = startIntegratedTunnel(ctx, loaded.Tunnel, db, sink)
			if err != nil {
				log.Fatalf("tunnel listener: %v", err)
			}
			defer configuredEdge.Close()
			managerOpts = append(managerOpts, routemanager.WithConnectorRoutes(configuredEdge))
			log.Printf("tunnel listener: %s (registry authorization required)", configuredEdge.address)
			go func() {
				<-configuredEdge.done
				if ctx.Err() == nil {
					log.Printf("tunnel listener stopped: %v", configuredEdge.err)
					stop()
				}
			}()
		}
	}
	manager, err := routemanager.New(ctx, *configPath, healthCheckInterval, managerOpts...)
	if err != nil {
		log.Fatalf("routes: %v", err)
	}
	cfg := manager.Config()
	if !reflect.DeepEqual(loaded.Tunnel, cfg.Tunnel) {
		log.Fatal("tunnel configuration changed during startup; restart with a stable config")
	}

	// EDGE-007: with tls.dns01 configured, Switchboard owns the origin
	// certificate lifecycle. The manager holds the single-writer lock on its
	// state directory until shutdown.
	var certificateManager *certs.Manager
	closeCertificates := func() {}
	if cfg.TLS.Enabled && cfg.TLS.DNS01 != nil {
		certificateManager, closeCertificates, err = startCertificateManager(ctx, &cfg, sink)
		if err != nil {
			log.Fatalf("certificate manager: %v", err)
		}
	}

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

	adminServer, err := startAdmin(&cfg, manager, sink, broker, certificateStatus(certificateManager))
	if err != nil {
		log.Fatalf("admin listener: %v", err)
	}
	servers := []*http.Server{adminServer}
	if cfg.TLS.Enabled {
		tlsServers, err := startTLS(&cfg, manager, sink, certificateManager)
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
	closeCertificates()
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

func startAdmin(cfg *config.Config, manager *routemanager.Manager, sink events.Sink, broker *events.Broker, certificate func() admin.CertificateStatus) (*http.Server, error) {
	listener, err := net.Listen("tcp", cfg.Admin.Listen)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Addr:              cfg.Admin.Listen,
		Handler:           admin.New(manager, sink, admin.WithEvents(broker), admin.WithCertificateStatus(certificate)),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		log.Printf("switchboard admin listening on %s", listener.Addr())
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
		log.Printf("switchboard listening on %s (plain HTTP)", listener.Addr())
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("public listener %s: %v", cfg.Listen, err)
		}
	}()
	return server, nil
}

// certificateProvider is the minimum the public listener needs from the
// certificate manager: one method, declared here by the consumer. The
// manager satisfies it, and a future implementation can replace it without
// touching this file's TLS contract.
type certificateProvider interface {
	GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
}

// certificateStatus projects the manager state into the console's shape.
// A nil manager (autocert mode) omits the certificate panel.
func certificateStatus(manager *certs.Manager) func() admin.CertificateStatus {
	if manager == nil {
		return nil
	}
	return func() admin.CertificateStatus {
		status := manager.Status()
		return admin.CertificateStatus{
			Host:        status.Host,
			Mode:        "ACME DNS-01",
			Valid:       status.Valid,
			NotAfter:    formatTime(status.NotAfter),
			LastOutcome: status.LastOutcome,
			LastError:   status.LastError,
			LastAttempt: formatTime(status.LastAttempt),
			NextAttempt: formatTime(status.NextAttempt),
			Pending:     status.PendingIntents,
		}
	}
}

// notificationSink builds the Telegram alert bridge when notify.telegram is
// configured. A nil sink means no channel is enabled: events still reach the
// log, the broker and the console.
func notificationSink(ctx context.Context, cfg *config.Config, logger *slog.Logger) (events.Sink, error) {
	if cfg.Notify == nil || cfg.Notify.Telegram == nil {
		return nil, nil
	}
	token, err := config.ResolveSecret(cfg.Notify.Telegram.Token)
	if err != nil {
		return nil, fmt.Errorf("notify.telegram.token: %w", err)
	}
	client, err := telegram.New(telegram.Config{Token: token, BaseURL: cfg.Notify.Telegram.APIBase})
	if err != nil {
		return nil, err
	}
	chats, err := notify.OpenFileChats(cfg.Notify.Telegram.ChatsFile)
	if err != nil {
		return nil, err
	}
	log.Printf("notifications: telegram alerts enabled for %d paired chats", len(chats.IDs()))
	return notify.NewAlertSink(ctx, client, chats, logger), nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// startCertificateManager builds the bounded DNS-01 stack and starts its
// renewal loop. The file store holds an exclusive lock on the state
// directory, so a second process pointed at the same state fails here
// instead of racing on the account, the generations or the DNS intents.
func startCertificateManager(ctx context.Context, cfg *config.Config, sink events.Sink) (*certs.Manager, func(), error) {
	token, err := config.ResolveSecret(cfg.TLS.DNS01.DNSToken)
	if err != nil {
		return nil, nil, fmt.Errorf("tls.dns01.dns_token: %w", err)
	}
	state, err := certs.OpenFileStore(cfg.TLS.DNS01.StateDir, cfg.TLS.DNS01.DirectoryURL)
	if err != nil {
		return nil, nil, err
	}
	closeState := func() { state.Close() }
	dnsProvider, err := certs.NewCloudflareClient(certs.CloudflareConfig{Token: token})
	if err != nil {
		closeState()
		return nil, nil, err
	}
	issuer, err := certs.NewACMEIssuer(certs.ACMEConfig{
		DirectoryURL: cfg.TLS.DNS01.DirectoryURL,
		Email:        cfg.TLS.Email,
		DNS:          dnsProvider,
	}, state)
	if err != nil {
		closeState()
		return nil, nil, err
	}
	var roots *x509.CertPool
	if cfg.TLS.DNS01.CertificateRootsFile != "" {
		bundle, err := os.ReadFile(cfg.TLS.DNS01.CertificateRootsFile)
		if err != nil {
			closeState()
			return nil, nil, fmt.Errorf("read tls.dns01.certificate_roots_file: %w", err)
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(bundle) {
			closeState()
			return nil, nil, errors.New("tls.dns01.certificate_roots_file contains no certificate")
		}
	}
	manager, err := certs.NewManager(certs.ManagerConfig{
		RootCAs: roots,
		Host:    cfg.TLS.Domains[0],
		Sink:    sink,
	}, state, issuer)
	if err != nil {
		closeState()
		return nil, nil, err
	}
	return manager, runCertificateManager(ctx, manager, closeState), nil
}

// startTLS serves public HTTPS. The autocert path also binds :80 for HTTP-01
// challenges; the DNS-01 path binds only :443, because validation never
// reaches this host.
func startTLS(cfg *config.Config, handler http.Handler, sink events.Sink, certificateManager *certs.Manager) ([]*http.Server, error) {
	if cfg.TLS.DNS01 != nil {
		return startManagedTLS(cfg, handler, sink, certificateManager)
	}
	return startAutocertTLS(cfg, handler, sink)
}

// startManagedTLS serves :443 with the configured DNS-01 manager. The client
// certificate policy is applied without the ACME ALPN exemption: this
// listener answers no tls-alpn-01 validation, so an acme-tls/1 connection
// without a client certificate is refused like any other.
func startManagedTLS(cfg *config.Config, handler http.Handler, sink events.Sink, certificateManager *certs.Manager) ([]*http.Server, error) {
	if certificateManager == nil {
		return nil, errors.New("dns-01 listener requires the certificate manager")
	}
	tlsConfig, handler, err := managedTLSConfig(cfg, handler, sink, certificateManager)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", ":443")
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Addr:              ":443",
		Handler:           handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		log.Printf("switchboard listening on :443 (HTTPS, ACME DNS-01 for %s)", cfg.TLS.Domains[0])
		if err := server.ServeTLS(listener, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("public listener :443: %v", err)
		}
	}()
	if status := certificateManager.Status(); !status.Valid {
		log.Printf("origin certificate not available yet: TLS handshakes fail closed until issuance succeeds")
	}
	return []*http.Server{server}, nil
}

// startAutocertTLS is the HTTP-01 path: HTTPS on :443 with autocert, plus a
// plain HTTP server on :80 that answers challenges and redirects everything
// else. Both ports bind synchronously: an occupied port must fail startup,
// not a goroutine.
func startAutocertTLS(cfg *config.Config, handler http.Handler, sink events.Sink) ([]*http.Server, error) {
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
		// autocert may answer a tls-alpn-01 challenge, which cannot present
		// a client certificate: opt into the exact exemption here, never in
		// the DNS-01 path.
		tlsConfig = originauth.ServerTLS(tlsConfig, roots, sink, originauth.AllowACMEValidation())
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

// managedTLSConfig applies the origin trust boundary without an ALPN exception.
func managedTLSConfig(cfg *config.Config, handler http.Handler, sink events.Sink, provider certificateProvider) (*tls.Config, http.Handler, error) {
	tlsConfig := &tls.Config{
		GetCertificate: provider.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}
	if cfg.TLS.OriginClientCAFile != "" {
		// Fail closed at startup: an unreadable or empty bundle must stop the
		// listener, never leave it open.
		bundle, err := os.ReadFile(cfg.TLS.OriginClientCAFile)
		if err != nil {
			return nil, nil, fmt.Errorf("read tls.origin_client_ca_file: %w", err)
		}
		roots, err := originauth.LoadPool(bundle)
		if err != nil {
			return nil, nil, fmt.Errorf("tls.origin_client_ca_file: %w", err)
		}
		tlsConfig = originauth.ServerTLS(tlsConfig, roots, sink)
		handler = originauth.RequireVerified(handler)
	}

	return tlsConfig, handler, nil
}

type certificateWorker interface{ Run(context.Context) }

// runCertificateManager owns cancellation and waits for DNS cleanup before
// releasing the single-writer storage lock. Calling close twice is safe.
func runCertificateManager(ctx context.Context, manager certificateWorker, release func()) func() {
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); manager.Run(workerCtx) }()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done; release() }) }
}
