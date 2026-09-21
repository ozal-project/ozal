package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/healthcheck"
	"github.com/octaviosantacruz/switchboard/internal/proxy"
)

const healthCheckInterval = 10 * time.Second

func main() {
	configPath := flag.String("config", "config.json", "path to routing config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	checker := healthcheck.New()
	checker.Start(ctx, cfg.Routes, healthCheckInterval)

	// gate stays a nil proxy.AuthGate (a genuinely nil interface) unless a
	// route actually needs auth — assigning a nil *auth.Gate to it
	// directly would instead produce a non-nil interface wrapping a nil
	// pointer, and proxy.go's "gate == nil" check would then miss it.
	var gate proxy.AuthGate
	var authGate *auth.Gate
	if hasAuthRequiredRoute(cfg.Routes) {
		hosts := make([]string, len(cfg.Routes))
		for i, r := range cfg.Routes {
			hosts[i] = r.Host
		}
		authGate, err = auth.NewGate(ctx, cfg.Auth, hosts)
		if err != nil {
			log.Fatalf("auth: %v", err)
		}
		gate = authGate
		log.Printf("OIDC login enabled (issuer: %s)", cfg.Auth.Issuer)
	}

	proxyHandler, err := proxy.New(cfg.Routes, checker, gate)
	if err != nil {
		log.Fatalf("proxy: %v", err)
	}
	handler := withAuthEndpoints(proxyHandler, authGate)

	log.Printf("routes:")
	for _, r := range cfg.Routes {
		log.Printf("  %s -> %s", r.Host, r.Target)
	}

	var servers []*http.Server
	if cfg.TLS.Enabled {
		servers = startTLS(cfg, handler)
	} else {
		servers = []*http.Server{startPlain(cfg, handler)}
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

func hasAuthRequiredRoute(routes []config.Route) bool {
	for _, r := range routes {
		if r.AuthRequired {
			return true
		}
	}
	return false
}

// withAuthEndpoints intercepts the /_/auth/* paths (login, callback,
// logout) regardless of Host, so they work the same way on every route's
// hostname; everything else falls through to the proxy. authGate may be
// nil if no route needs auth, in which case those paths 404 like anything
// else unmatched by proxyHandler.
func withAuthEndpoints(proxyHandler http.Handler, authGate *auth.Gate) http.Handler {
	if authGate == nil {
		return proxyHandler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_/auth/login":
			authGate.HandleLogin(w, r)
		case "/_/auth/callback":
			authGate.HandleCallback(w, r)
		case "/_/auth/logout":
			authGate.HandleLogout(w, r)
		default:
			proxyHandler.ServeHTTP(w, r)
		}
	})
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
