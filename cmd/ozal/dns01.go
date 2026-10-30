package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
)

// runDNS01 dispatches `ozal dns01 <command>`: `check` (preflight) or `try`.
func runDNS01(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "try":
			return runDNS01Try(ctx, args[1:], out)
		case "check":
			return runDNS01Check(ctx, args[1:], out)
		}
	}
	return errors.New("usage: ozal dns01 check -config PATH [-resolver HOST:PORT] | ozal dns01 try -config PATH [-allow-production] [-timeout 5m]")
}

// runDNS01Try issues one certificate through the configured DNS-01 provider
// and throws it away. It exists to prove the whole chain (provider, DNS
// delegation, propagation, the CA's validation) without binding :443, without
// touching the production state directory and without a certificate the edge
// would ever serve.
func runDNS01Try(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("dns01 try", flag.ContinueOnError)
	fs.SetOutput(out)
	path := fs.String("config", "config.json", "path to routing config")
	allowProduction := fs.Bool("allow-production", false, "allow a production CA directory (counts against its rate limits)")
	timeout := fs.Duration("timeout", 5*time.Minute, "overall limit for the issuance")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	return tryDNS01(ctx, cfg, *allowProduction, *timeout, out, newACMEObtainer)
}

// obtainer is the one thing the rehearsal needs from an issuer.
type obtainer interface {
	Obtain(ctx context.Context, names []string, key crypto.Signer) (certs.StoredGeneration, error)
}

func newACMEObtainer(cfg certs.ACMEConfig, state *certs.FileStore) (obtainer, error) {
	return certs.NewACMEIssuer(cfg, state)
}

func tryDNS01(ctx context.Context, cfg *config.Config, allowProduction bool, timeout time.Duration, out io.Writer, newIssuer func(certs.ACMEConfig, *certs.FileStore) (obtainer, error)) error {
	var err error
	if cfg.TLS.DNS01 == nil || !cfg.TLS.Enabled {
		return errors.New("tls.dns01 is not configured: nothing to try")
	}
	if err := refuseProductionDirectory(cfg.TLS.DNS01.DirectoryURL, allowProduction); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// A throwaway state directory: the production account, generations and
	// intents stay untouched, and the staging account is created fresh.
	stateDir, err := os.MkdirTemp("", "ozal-dns01-try-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stateDir)
	state, err := certs.OpenFileStore(stateDir, cfg.TLS.DNS01.DirectoryURL)
	if err != nil {
		return err
	}
	defer state.Close()

	dnsProvider, stopDNS := certs.DNSProvider(nil), func() {}
	if cfg.TLS.DNS01.DNSProvider == "builtin" {
		dnsProvider, stopDNS, err = startBuiltinDNS(ctx, cfg.TLS.DNS01)
	} else {
		dnsProvider, err = newDNSProvider(cfg.TLS.DNS01)
	}
	if err != nil {
		return err
	}
	defer stopDNS()

	var roots *x509.CertPool
	if file := cfg.TLS.DNS01.CertificateRootsFile; file != "" {
		bundle, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read tls.dns01.certificate_roots_file: %w", err)
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(bundle) {
			return errors.New("tls.dns01.certificate_roots_file contains no certificate")
		}
	}

	account, err := externalAccount(cfg.TLS.DNS01)
	if err != nil {
		return err
	}
	issuer, err := newIssuer(certs.ACMEConfig{
		DirectoryURL:    cfg.TLS.DNS01.DirectoryURL,
		Email:           cfg.TLS.Email,
		DNS:             dnsProvider,
		Delegations:     cfg.TLS.DNS01.ChallengeDelegation,
		ExternalAccount: account,
	}, state)
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	provider := cfg.TLS.DNS01.DNSProvider
	fmt.Fprintf(out, "dns01 try: provider=%s names=%s directory=%s\n", provider, strings.Join(cfg.TLS.Domains, ","), cfg.TLS.DNS01.DirectoryURL)
	started := time.Now()
	gen, err := issuer.Obtain(ctx, cfg.TLS.Domains, key)
	if err != nil {
		return fmt.Errorf("issuance failed after %s: %w", time.Since(started).Round(time.Second), err)
	}
	pair, err := tls.X509KeyPair(gen.CertPEM, gen.KeyPEM)
	if err != nil || len(pair.Certificate) == 0 {
		return errors.New("the CA returned a certificate that does not match the key")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	for _, name := range cfg.TLS.Domains {
		if !leafCovers(leaf, name) {
			return fmt.Errorf("the issued certificate does not cover %q", name)
		}
	}
	verified := "chain NOT verified (set tls.dns01.certificate_roots_file to verify a staging chain)"
	if roots != nil {
		if _, err := certs.ParseGeneration(gen.CertPEM, gen.KeyPEM, cfg.TLS.Domains, time.Now(), roots); err != nil {
			return fmt.Errorf("the issued certificate does not verify: %w", err)
		}
		verified = "chain verified against certificate_roots_file"
	}
	fmt.Fprintf(out, "dns01 try: OK in %s; issuer=%q names=%v not_after=%s; %s\n",
		time.Since(started).Round(time.Second), leaf.Issuer.CommonName, leaf.DNSNames, leaf.NotAfter.UTC().Format(time.RFC3339), verified)
	fmt.Fprintln(out, "dns01 try: the certificate was discarded; nothing was written to the production state and :443 was not touched")
	return nil
}

func leafCovers(leaf *x509.Certificate, name string) bool {
	for _, n := range leaf.DNSNames {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// refuseProductionDirectory keeps a rehearsal from spending the production
// CA's rate limits (duplicate-certificate and failed-validation budgets).
func refuseProductionDirectory(directoryURL string, allow bool) error {
	u, err := url.Parse(directoryURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("tls.dns01.directory_url %q is not a valid URL", directoryURL)
	}
	if allow || strings.Contains(strings.ToLower(u.Host+u.Path), "staging") {
		return nil
	}
	return fmt.Errorf("refusing %s: it does not look like a staging CA, and a rehearsal would spend production rate limits; point directory_url at the staging directory or pass -allow-production", u.Host)
}
