package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

// runValidate loads and validates a candidate config without binding
// listeners: deployment runbooks use it to check a file before installing it.
// It never resolves or prints secrets (references stay references).
func runValidate(_ context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(out)
	path := fs.String("config", "config.json", "path to routing config")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	proxies, err := config.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return err
	}
	protected := 0
	tunnels := 0
	for _, r := range cfg.Routes {
		if r.RequiresAuth() {
			protected++
		}
		if r.Connector != "" {
			tunnels++
		}
	}
	adminListen := cfg.Admin.Listen
	if adminListen == "" {
		adminListen = config.DefaultAdminListen
	}
	fmt.Fprintf(out, "config OK: routes=%d protected=%d tunnel=%d tls=%t dns01=%t trusted_proxies=%d admin=%s\n",
		len(cfg.Routes), protected, tunnels, cfg.TLS.Enabled, cfg.TLS.DNS01 != nil, len(proxies), adminListen)
	return nil
}
