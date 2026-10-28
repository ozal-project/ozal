package main

import (
	"context"
	"net/http"

	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/pages/deployapi"
	"github.com/switchboard-project/switchboard/internal/pipeline"
)

type publicConfigManager interface {
	http.Handler
	Config() config.Config
}

// The composition root owns the deploy adapter. Project trust and proxy-IP
// policy are read from current snapshots; limit and replay state survive reload.
func publicPagesHandler(ctx context.Context, cfg config.Config, manager publicConfigManager, store deployapi.Store, replays deployapi.ReplayStore, sink events.Sink, audit events.AuditAppender) (http.Handler, error) {
	if cfg.Pages == nil || cfg.Pages.Deploy == nil {
		return manager, nil
	}
	settings := cfg.Pages.Deploy
	options := deployapi.Options{Host: settings.Host, Audience: settings.Audience, Projects: func() []config.Project { return manager.Config().Projects }, Store: store, Replays: replays, Sink: sink,
		Verifier: deployapi.NewGitHubVerifier(ctx, settings.Audience, nil), Audit: audit}
	if cfg.Audit != nil && cfg.Audit.Includes("admin") {
		options.AuditEnabled = true
		options.AuditRequired = cfg.Audit.AdminRequired()
	}
	deploy, err := deployapi.New(options)
	if err != nil {
		return nil, err
	}
	trusted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefixes, err := config.ParseTrustedProxies(manager.Config().TrustedProxies)
		if err != nil {
			http.Error(w, "deployment policy unavailable", http.StatusServiceUnavailable)
			return
		}
		pipeline.TrustWithProxies(prefixes)(deploy).ServeHTTP(w, r)
	})
	return deployapi.RouteHost(settings.Host, trusted, manager), nil
}
