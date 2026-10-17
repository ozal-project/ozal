package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/octaviosantacruz/switchboard/internal/admin"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/routemanager"
)

const healthCheckInterval = 10 * time.Second

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := runMigrate(context.Background(), os.Args[2:], os.Stdout); err != nil {
			log.Fatalf("migrate: %v", err)
		}
		return
	}

	configPath := flag.String("config", "config.json", "path to routing config")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	manager, err := routemanager.New(ctx, *configPath, healthCheckInterval)
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
				if err := manager.Reload(); err != nil {
					log.Printf("audit actor=signal:SIGHUP action=config.reload result=rejected error=%q", err)
					continue
				}
				log.Printf("audit actor=signal:SIGHUP action=config.reload result=accepted")
			}
		}
	}()

	log.Printf("routes:")
	for _, r := range cfg.Routes {
		log.Printf("  %s -> %s", r.Host, r.Destination())
	}

	adminServer, err := startAdmin(&cfg, manager)
	if err != nil {
		log.Fatalf("admin listener: %v", err)
	}
	servers := []*http.Server{adminServer}
	if cfg.TLS.Enabled {
		servers = append(servers, startTLS(&cfg, manager)...)
	} else {
		servers = append(servers, startPlain(&cfg, manager))
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

func startAdmin(cfg *config.Config, manager *routemanager.Manager) (*http.Server, error) {
	listener, err := net.Listen("tcp", cfg.Admin.Listen)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Addr:              cfg.Admin.Listen,
		Handler:           admin.New(manager, nil),
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

func startPlain(cfg *config.Config, handler http.Handler) *http.Server {
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		log.Printf("switchboard listening on %s (plain HTTP)", cfg.Listen)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen %s: %v", cfg.Listen, err)
		}
	}()
	return server
}

// startTLS serves HTTPS on :443 with certificates issued automatically via
// ACME, plus a plain HTTP server on :80 that answers ACME HTTP-01
// challenges and redirects everything else to HTTPS.
func startTLS(cfg *config.Config, handler http.Handler) []*http.Server {
	manager := &autocert.Manager{
		Cache:      autocert.DirCache(cfg.TLS.CacheDir),
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.TLS.Domains...),
		Email:      cfg.TLS.Email,
	}

	httpsServer := &http.Server{
		Addr:              ":443",
		Handler:           handler,
		TLSConfig:         manager.TLSConfig(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		log.Printf("switchboard listening on :443 (HTTPS, ACME domains: %v)", cfg.TLS.Domains)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen :443: %v", err)
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
		if err := challengeServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen :80: %v", err)
		}
	}()

	return []*http.Server{httpsServer, challengeServer}
}
