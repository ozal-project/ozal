package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/ozal-project/ozal/internal/acmedns"
	"github.com/ozal-project/ozal/internal/admin"
	"github.com/ozal-project/ozal/internal/audit"
	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/domaincheck/probe"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/githubapp"
	"github.com/ozal-project/ozal/internal/notify"
	"github.com/ozal-project/ozal/internal/notify/telegram"
	"github.com/ozal-project/ozal/internal/originauth"
	"github.com/ozal-project/ozal/internal/pages"
	"github.com/ozal-project/ozal/internal/routemanager"
	"github.com/ozal-project/ozal/internal/store/sqlite"
	"github.com/ozal-project/ozal/internal/tunnel"
)

const healthCheckInterval = 10 * time.Second
const auditStartupFailure = "durable audit journal unavailable; verify with 'ozal audit verify'; if the final record is incomplete, stop Ozal and use 'ozal audit repair -dir ABSOLUTE_PATH'"

// subcommands are the one-shot commands run as `ozal <name> ...`. Each
// gets a context that SIGINT/SIGTERM cancel, so none can outlive a stop signal.
func subcommands() map[string]func(context.Context, []string, io.Writer) error {
	return map[string]func(context.Context, []string, io.Writer) error{
		"app":          runApp,
		"project":      runProject,
		"audit":        runAudit,
		"completion":   runCompletion,
		"console":      runConsole,
		"db":           runDB,
		"doctor":       runDoctor,
		"dns01":        runDNS01,
		"init":         runInit,
		"install":      runInstall,
		"github":       runGitHub,
		"tunnel":       runTunnel,
		"tunnel-spike": runTunnelSpike,
		"upgrade":      runUpgrade,
		"connector":    runConnector,
		"migrate":      runMigrate,
		"notify":       runNotify,
		"policy":       runPolicy,
		"site":         runSite,
		"schema":       runSchema,
		"setup":        runSetup,
		"runtime":      runRuntime,
		"user":         runUser,
		"validate":     runValidate,
		"version":      runVersion,
	}
}

func main() {
	if len(os.Args) > 1 {
		if os.Args[1] == "--version" || os.Args[1] == "-version" {
			if err := runVersion(context.Background(), nil, os.Stdout); err != nil {
				log.Fatal(err)
			}
			return
		}
		if run, ok := subcommands()[os.Args[1]]; ok {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			err := run(ctx, os.Args[2:], os.Stdout)
			stop() // log.Fatalf exits without running deferred calls
			if err != nil {
				// upgrade's results are distinct statuses for scripts.
				var exit *upgradeExit
				if errors.As(err, &exit) {
					log.Printf("%s: %v", os.Args[1], err)
					os.Exit(exit.code)
				}
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
	alertSink, pairer, err := notifications(ctx, loaded, logger)
	if err != nil {
		log.Fatalf("notifications: %v", err)
	}
	sink := events.Multi{events.SlogSink{Logger: logger}, broker}
	if alertSink != nil {
		sink = append(sink, alertSink)
	}
	managerOpts := []routemanager.Option{routemanager.WithEventSink(sink)}
	var auditWriter *audit.Writer
	var auditAppender events.AuditAppender
	var auditStatus func() admin.AuditStatus
	if loaded.Audit != nil {
		writer, openErr := audit.Open(ctx, loaded.Audit.Dir, audit.Options{MaxSegmentBytes: loaded.Audit.MaxSegmentBytes})
		if openErr != nil {
			if errors.Is(openErr, audit.ErrIncompleteTail) ||
				requiresDurableAccessAudit(*loaded) ||
				requiresDurableAdminAudit(*loaded) {
				log.Fatal(auditStartupFailure)
			}
			reporter := newAuditFailureReporter(nil, logger, openErr)
			reporter.report(openErr)
			auditAppender = reporter
			auditStatus = reporter.Status
		} else {
			auditWriter = writer
			reporter := newAuditFailureReporter(writer, logger, nil)
			auditAppender = reporter
			auditStatus = reporter.Status
			defer func() {
				if err := writer.Close(); err != nil {
					logger.Error("close durable audit writer", "failure_class", "storage")
				}
			}()
		}
		if loaded.Audit.Includes("access") {
			managerOpts = append(managerOpts, routemanager.WithAuditAppender(auditAppender))
		}
	}
	var configuredEdge *configuredTunnel
	var pagesStore *pages.Store
	var deployReplays *sqlite.DeployReplays
	if loaded.Tunnel != nil && *dbPath == "" {
		log.Fatal("tunnel configuration requires -db pointing at migrated state")
	}
	if loaded.Pages != nil && *dbPath == "" {
		log.Fatal("pages configuration requires -db pointing at migrated state")
	}
	adminOpts := []admin.Option{admin.WithGitHubRepositories(githubapp.New())}
	if loaded.Audit != nil && loaded.Audit.Includes("admin") {
		adminOpts = append(adminOpts, admin.WithAdminMutationAudit(auditAppender, loaded.Audit.AdminRequired()))
	}
	if auditStatus != nil {
		adminOpts = append(adminOpts, admin.WithAuditStatus(auditStatus))
	}
	if pairer != nil {
		// CLI-001: `ozal notify pair` asks the process over the admin
		// socket; the process alone reads the bot's updates.
		adminOpts = append(adminOpts, admin.WithPairing(pairer))
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
		managerOpts = append(managerOpts, routemanager.WithSessionBackend(func(lifetime auth.Lifetime) auth.SessionBackend {
			return sqlite.NewSessions(db, lifetime)
		}))
		log.Printf("sessions: persistent store at %s", *dbPath)
		// ID-003: users and passkeys share the state database, so revoking
		// a passkey and ending its sessions is one transaction.
		identity := sqlite.NewIdentity(db)
		managerOpts = append(managerOpts, routemanager.WithPasskeyDirectory(identity))
		adminOpts = append(adminOpts, admin.WithPeople(identity))
		// UI-007: administrators have their own passkeys, apart from users.
		administrators := sqlite.NewAdmins(db)
		adminOpts = append(adminOpts, admin.WithIdentities(administrators))
		// A fresh install needs no command: until the first administrator
		// exists, a one-use setup link is kept in a file only root and the
		// service account can read. It is never written to the log: logs are
		// read by more people and shipped elsewhere.
		setupLink := filepath.Join(filepath.Dir(*dbPath), "setup-link")
		if token, _, err := admin.IssueFirstRun(ctx, administrators); err != nil {
			log.Printf("console: could not prepare the first-administrator link: %v", err)
		} else if token != "" {
			if err := writeSetupLink(setupLink, firstRunNotice(loaded.Admin.Listen, token)); err != nil {
				log.Printf("console: could not store the setup link (%v); run: sudo ozal console enrol", err)
			} else {
				log.Printf("console: no administrator yet. The setup link is in %s (root only, valid %d minutes): sudo cat %s", setupLink, int(admin.FirstRunTTL.Minutes()), setupLink)
			}
		} else {
			_ = os.Remove(setupLink)
		}
		adminOpts = append(adminOpts, admin.WithEnrolCommand(hostEnrolCommand(*configPath)))
		// CLI-001: the connector registry is managed through the admin API
		// (changes on the unix socket only); the edge revalidates live
		// attachments against the same table.
		adminOpts = append(adminOpts, admin.WithConnectors(tunnel.NewRegistry(db)))
		if loaded.Pages != nil {
			// PAGES-002: deployments live under pages.dir and their history
			// in the state database; site routes read the live pointer.
			store, err := pages.NewStore(ctx, db, loaded.Pages.Dir, loaded.Pages.Keep, pages.DefaultLimits)
			if err != nil {
				log.Fatalf("pages: %v", err)
			}
			managerOpts = append(managerOpts, routemanager.WithSites(store))
			adminOpts = append(adminOpts, admin.WithPages(store))
			pagesStore = store
			deployReplays = sqlite.NewDeployReplays(db)
			log.Printf("pages: deployments under %s", loaded.Pages.Dir)
		}
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
	if !reflect.DeepEqual(loaded.Tunnel, cfg.Tunnel) || !reflect.DeepEqual(loaded.Pages, cfg.Pages) {
		log.Fatal("tunnel or pages configuration changed during startup; restart with a stable config")
	}
	if !reflect.DeepEqual(loaded.Audit, cfg.Audit) {
		log.Fatal("audit configuration changed during startup; restart with a stable config")
	}

	// EDGE-007: with tls.dns01 configured, Ozal owns the origin
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
				event.ConfigSHA256 = manager.ConfigDigest()
				sink.Emit(event)
			}
		}
	}()

	log.Printf("routes:")
	for _, r := range cfg.Routes {
		log.Printf("  %s -> %s", r.Host, r.Destination())
	}

	// UI-006: the console checks that a hostname reaches this edge with a
	// one-time token the public listener answers before routing.
	probeTokens := probe.NewTokens(nil)
	adminOpts = append(adminOpts, admin.WithDomains(probe.NewService(probeTokens)))
	adminServer, err := startAdmin(&cfg, manager, sink, broker, certificateStatus(certificateManager), adminOpts...)
	if err != nil {
		log.Fatalf("admin listener: %v", err)
	}
	servers := []*http.Server{adminServer}
	publicHandler, err := publicPagesHandler(ctx, cfg, manager, pagesStore, deployReplays, sink, auditAppender)
	if err != nil {
		log.Fatal("public deploy handler could not be configured")
	}
	publicHandler = probe.Handler(probeTokens, publicHandler)
	if cfg.TLS.Enabled {
		tlsServers, err := startTLS(&cfg, publicHandler, sink, certificateManager, manager.Config)
		if err != nil {
			log.Fatalf("public listener: %v", err)
		}
		servers = append(servers, tlsServers...)
	} else {
		plainServer, err := startPlain(&cfg, publicHandler)
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
	if auditWriter != nil {
		if err := auditWriter.Close(); err != nil {
			logger.Error("close durable audit writer", "failure_class", "storage")
		}
	}
}

func requiresDurableAccessAudit(cfg config.Config) bool {
	if cfg.Audit == nil || !cfg.Audit.Includes("access") {
		return false
	}
	for _, route := range cfg.Routes {
		if route.AuditRequired() {
			return true
		}
	}
	return false
}

func requiresDurableAdminAudit(cfg config.Config) bool {
	return cfg.Audit != nil && cfg.Audit.Includes("admin") && cfg.Audit.AdminRequired()
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

func startAdmin(cfg *config.Config, manager *routemanager.Manager, sink events.Sink, broker *events.Broker, certificate func() admin.CertificateStatus, extra ...admin.Option) (*http.Server, error) {
	listener, err := net.Listen("tcp", cfg.Admin.Listen)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Addr:              cfg.Admin.Listen,
		Handler:           admin.New(manager, sink, append([]admin.Option{admin.WithEvents(broker), admin.WithCertificateStatus(certificate)}, extra...)...),
		ConnContext:       admin.LocalConn,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       idleTimeout,
	}
	if cfg.Admin.Socket != "" {
		// CLI-001: the same handler on a unix socket; Shutdown closes both
		// listeners and the socket file goes with its listener.
		socket, err := listenAdminSocket(cfg.Admin.Socket)
		if err != nil {
			listener.Close()
			return nil, err
		}
		go func() {
			log.Printf("ozal admin listening on unix:%s", cfg.Admin.Socket)
			if err := server.Serve(socket); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("admin socket %s: %v", cfg.Admin.Socket, err)
			}
		}()
	}
	go func() {
		log.Printf("ozal admin listening on %s", listener.Addr())
		if cfg.Admin.Token == "" {
			if cfg.Admin.Socket != "" {
				log.Printf("admin mutations over TCP disabled (no admin.token); the admin socket accepts them")
			} else {
				log.Printf("admin mutations disabled: set admin.token in config")
			}
		}
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("admin listener %s: %v", cfg.Admin.Listen, err)
		}
	}()
	return server, nil
}

// listenAdminSocket binds the admin unix socket with mode 0600. A socket
// left by a crashed process is replaced; any other file at the path stops
// startup. The parent directory should be 0700 (systemd RuntimeDirectory)
// so the socket is never reachable between bind and chmod.
func listenAdminSocket(path string) (net.Listener, error) {
	// The socket's file mode is its credential; Windows does not enforce it,
	// so an admin socket there would answer any local user.
	if runtime.GOOS == "windows" {
		return nil, errors.New("admin.socket is not supported on Windows: its unix file mode is the credential")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("admin socket %s exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
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
		log.Printf("ozal listening on %s (plain HTTP)", listener.Addr())
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
			Host:        strings.Join(status.Names, ", "),
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

// notifications builds the Telegram alert bridge and the chat pairing when
// notify.telegram is configured. Both share one chats file, so a newly
// paired chat gets the alert that announces it. A nil sink means no channel
// is enabled: events still reach the log, the broker and the console.
func notifications(ctx context.Context, cfg *config.Config, logger *slog.Logger) (events.Sink, *notify.Pairer, error) {
	if cfg.Notify == nil || cfg.Notify.Telegram == nil {
		return nil, nil, nil
	}
	token, err := config.ResolveSecret(cfg.Notify.Telegram.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("notify.telegram.token: %w", err)
	}
	client, err := telegram.New(telegram.Config{Token: token, BaseURL: cfg.Notify.Telegram.APIBase})
	if err != nil {
		return nil, nil, err
	}
	chats, err := notify.OpenFileChats(cfg.Notify.Telegram.ChatsFile)
	if err != nil {
		return nil, nil, err
	}
	log.Printf("notifications: telegram alerts enabled for %d paired chats", len(chats.IDs()))
	pairer := notify.NewPairer(ctx, client.Inbox(10*time.Second), chats, logger)
	return notify.NewAlertSink(ctx, client, chats, logger), pairer, nil
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
	var (
		dnsProvider certs.DNSProvider
		stopDNS     = func() {}
		err         error
	)
	if cfg.TLS.DNS01.DNSProvider == "builtin" {
		dnsProvider, stopDNS, err = startBuiltinDNS(ctx, cfg.TLS.DNS01)
	} else {
		dnsProvider, err = newDNSProvider(cfg.TLS.DNS01)
	}
	if err != nil {
		return nil, nil, err
	}
	state, err := certs.OpenFileStore(cfg.TLS.DNS01.StateDir, cfg.TLS.DNS01.DirectoryURL)
	if err != nil {
		stopDNS()
		return nil, nil, err
	}
	// The DNS server lives exactly as long as the state it answers for.
	closeState := func() { stopDNS(); state.Close() }
	issuer, err := certs.NewACMEIssuer(certs.ACMEConfig{
		DirectoryURL: cfg.TLS.DNS01.DirectoryURL,
		Email:        cfg.TLS.Email,
		DNS:          dnsProvider,
		Delegations:  cfg.TLS.DNS01.ChallengeDelegation,
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
		RootCAs:  roots,
		Host:     cfg.TLS.Domains[0],
		AltNames: cfg.TLS.Domains[1:],
		Sink:     sink,
	}, state, issuer)
	if err != nil {
		closeState()
		return nil, nil, err
	}
	// Before any listener exists, so its first status check is true.
	manager.LoadStored(ctx)
	return manager, runCertificateManager(ctx, manager, closeState), nil
}

// startTLS serves public HTTPS. The autocert path also binds :80 for HTTP-01
// challenges; the DNS-01 path binds only :443, because validation never
// reaches this host.
func startTLS(cfg *config.Config, handler http.Handler, sink events.Sink, certificateManager *certs.Manager, current func() config.Config) ([]*http.Server, error) {
	if cfg.TLS.DNS01 != nil {
		return startManagedTLS(cfg, handler, sink, certificateManager)
	}
	return startAutocertTLS(cfg, handler, sink, current)
}

// routeHostPolicy allows a certificate request only for a configured
// certificate name or the exact hostname of a route in the current
// configuration. Anything else is refused before the CA is contacted, so a
// visitor cannot make the edge order certificates for names it does not serve.
func routeHostPolicy(configured []string, current func() config.Config) autocert.HostPolicy {
	return func(_ context.Context, host string) error {
		routes := current().Routes
		hosts := make([]string, 0, len(routes))
		for _, route := range routes {
			hosts = append(hosts, route.Host)
		}
		if !certs.AutomaticName(host, configured, hosts) {
			return fmt.Errorf("certificate for %q is not allowed: no configured route serves it", host)
		}
		return nil
	}
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
		log.Printf("ozal listening on :443 (HTTPS, ACME DNS-01 for %s)", strings.Join(cfg.TLS.Domains, ", "))
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
// not a goroutine. Certificates follow the current route table (UI-006): a
// hostname added in the console gets one on its first visit, no restart.
func startAutocertTLS(cfg *config.Config, handler http.Handler, sink events.Sink, current func() config.Config) ([]*http.Server, error) {
	manager := newAutocertManager(cfg, current)
	tlsConfig, handler, err := autocertTLSConfig(cfg, handler, sink, manager)
	if err != nil {
		return nil, err
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
		log.Printf("ozal listening on :443 (HTTPS, ACME domains: %v)", cfg.TLS.Domains)
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
		log.Printf("ozal listening on :80 (ACME challenges + HTTPS redirect)")
		if err := challengeServer.Serve(challengeListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("public listener :80: %v", err)
		}
	}()

	return []*http.Server{httpsServer, challengeServer}, nil
}

// newAutocertManager shares the HTTP-01 certificate policy and cache wiring.
func newAutocertManager(cfg *config.Config, current func() config.Config) *autocert.Manager {
	return &autocert.Manager{
		Cache:      autocert.DirCache(cfg.TLS.CacheDir),
		Prompt:     autocert.AcceptTOS,
		HostPolicy: routeHostPolicy(cfg.TLS.Domains, current),
		Email:      cfg.TLS.Email,
	}
}

// autocertTLSConfig applies origin trust to the HTTP-01 listener.
func autocertTLSConfig(cfg *config.Config, handler http.Handler, sink events.Sink, manager *autocert.Manager) (*tls.Config, http.Handler, error) {
	tlsConfig := manager.TLSConfig()
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
		// autocert may answer a tls-alpn-01 challenge, which cannot present
		// a client certificate: opt into the exact exemption here, never in
		// the DNS-01 path.
		tlsConfig = originauth.ServerTLS(tlsConfig, roots, sink, originauth.AllowACMEValidation())
		handler = originauth.RequireVerified(handler)
	}

	return tlsConfig, handler, nil
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

// hostEnrolCommand describes how to enrol on this host from its own paths.
func hostEnrolCommand(configPath string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}
	return enrolCommand(exe, configPath, func(name string) string {
		found, err := exec.LookPath(name)
		if err != nil {
			return ""
		}
		if resolved, err := filepath.EvalSymlinks(found); err == nil {
			return resolved
		}
		return found
	})
}

// firstRunNotice is the log line that replaces `console enrol` on a new
// install: the link for the first administrator and how to reach it remotely.
func firstRunNotice(adminListen, token string) string {
	base := passkeyOrigin(forwardedURL(adminListen))
	port := "9090"
	if u, err := url.Parse(base); err == nil && u.Port() != "" {
		port = u.Port()
	}
	return fmt.Sprintf("console: no administrator yet. Open this link once (valid %d minutes): %s/overview#enrol=%s\n"+
		"console: from another computer, forward the port first: ssh -N -L %s:127.0.0.1:%s USER@THIS_HOST",
		int(admin.FirstRunTTL.Minutes()), base, url.QueryEscape(token), port, port)
}

// writeSetupLink stores the link with mode 0600, replacing any older one.
func writeSetupLink(path, text string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".setup-link-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(text + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// newDNSProvider builds the DNS-01 adapter the configuration selects. Both
// adapters satisfy certs.DNSProvider, so the ACME flow is the same; secrets
// are resolved here, at composition time, and never stored in the config.
func newDNSProvider(dns01 *config.DNS01) (certs.DNSProvider, error) {
	switch dns01.DNSProvider {
	case "rfc2136":
		r := dns01.RFC2136
		resolved, err := config.ResolveSecret(r.Secret)
		if err != nil {
			return nil, fmt.Errorf("tls.dns01.rfc2136.secret: %w", err)
		}
		secret, err := base64.StdEncoding.DecodeString(strings.TrimSpace(resolved))
		if err != nil {
			return nil, errors.New("tls.dns01.rfc2136.secret is not base64")
		}
		return certs.NewRFC2136Client(certs.RFC2136Config{
			Server: r.Server, Zone: r.Zone, KeyName: r.KeyName, Algorithm: r.Algorithm, Secret: secret,
		})
	case "hetzner":
		h := dns01.Hetzner
		token, err := config.ResolveSecret(h.Token)
		if err != nil {
			return nil, fmt.Errorf("tls.dns01.hetzner.token: %w", err)
		}
		return certs.NewHetznerClient(certs.HetznerConfig{Token: token, Zone: h.Zone})
	case "builtin":
		return nil, errors.New("tls.dns01 builtin starts its own server and is not built here")
	case "", "cloudflare":
		token, err := config.ResolveSecret(dns01.DNSToken)
		if err != nil {
			return nil, fmt.Errorf("tls.dns01.dns_token: %w", err)
		}
		return certs.NewCloudflareClient(certs.CloudflareConfig{Token: token})
	default:
		return nil, fmt.Errorf("tls.dns01 dns_provider %q is not supported", dns01.DNSProvider)
	}
}

// startBuiltinDNS binds Ozal's own authoritative server for the challenge
// zone and returns the certificate adapter that publishes into it. Binding
// failures surface here, at startup, rather than as a certificate that never
// validates. The returned function stops the server.
func startBuiltinDNS(ctx context.Context, dns01 *config.DNS01) (certs.DNSProvider, func(), error) {
	b := dns01.Builtin
	zone, err := acmedns.NewZone(b.Zone, b.Nameservers, b.Contact, uint32(time.Now().Unix()))
	if err != nil {
		return nil, nil, err
	}
	store := acmedns.NewStore(zone)
	provider, err := certs.NewBuiltinDNS(store)
	if err != nil {
		return nil, nil, err
	}
	server, err := acmedns.Start(ctx, acmedns.Config{Zone: zone, Store: store, Addr: b.Listen})
	if err != nil {
		return nil, nil, err
	}
	return provider, func() { server.Close() }, nil
}
