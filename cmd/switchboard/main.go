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

	handler, err := proxy.New(cfg.Routes, checker)
	if err != nil {
		log.Fatalf("proxy: %v", err)
	}

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

func startPlain(cfg *config.Config, handler http.Handler) *http.Server {
	server := &http.Server{Addr: cfg.Listen, Handler: handler}
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
		Addr:      ":443",
		Handler:   handler,
		TLSConfig: manager.TLSConfig(),
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
	}
	go func() {
		log.Printf("switchboard listening on :80 (ACME challenges + HTTPS redirect)")
		if err := challengeServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen :80: %v", err)
		}
	}()

	return []*http.Server{httpsServer, challengeServer}
}
