package admin

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/switchboard-project/switchboard/internal/certs"
	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/routemanager"
)

type applicationWriter interface {
	PutApplication(config.Route, string) error
}

type applicationRequest struct {
	AccessMode string       `json:"access_mode"`
	Route      config.Route `json:"route"`
	Expected   string       `json:"expected"`
}

func (h *Handler) setupStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.manager.Config()
	methods := []string{}
	if cfg.Auth.Passkeys != nil {
		methods = append(methods, "passkey")
	}
	if cfg.Auth.Issuer != "" {
		methods = append(methods, "oidc")
	}
	writeJSON(w, 200, map[string]any{
		"config_sha256": h.manager.ConfigDigest(), "methods": methods,
		"pages_enabled": cfg.Pages != nil, "tls_enabled": cfg.TLS.Enabled,
		"routes": cfg.Routes, "login_url": loginURL(cfg),
		"admin_socket_configured": cfg.Admin.Socket != "",
		"tls_mode":                tlsMode(cfg), "dns": dnsSettings(cfg),
	})
}

// tlsMode says how this edge gets certificates: "off" (plain HTTP, TLS in
// front of it), "automatic" (HTTP-01 for every configured route) or "dns01"
// (one managed certificate whose names bound the hostnames it serves).
func tlsMode(cfg config.Config) string {
	switch {
	case !cfg.TLS.Enabled:
		return "off"
	case cfg.TLS.DNS01 != nil:
		return "dns01"
	default:
		return "automatic"
	}
}

func dnsSettings(cfg config.Config) map[string]any {
	settings := map[string]any{"target": "", "proxied": false, "provider": ""}
	if cfg.DNS != nil {
		settings["target"], settings["proxied"] = cfg.DNS.Target, cfg.DNS.Proxied
		if cfg.DNS.Cloudflare != nil {
			settings["provider"] = "cloudflare"
		}
	}
	return settings
}

func loginURL(cfg config.Config) string {
	if cfg.Auth.Passkeys != nil {
		return cfg.Auth.Passkeys.LoginURL
	}
	return cfg.Auth.RedirectURL
}

func (h *Handler) planApplication(w http.ResponseWriter, r *http.Request) {
	req, ok := h.applicationInput(w, r)
	if !ok {
		return
	}
	cfg := h.manager.Config()
	candidate, err := config.WithApplication(cfg, req.Route)
	if err != nil {
		writeError(w, 422, "application is invalid: "+err.Error())
		return
	}
	var normalized config.Route
	for _, rt := range candidate.Routes {
		host, _ := config.NormalizeHost(req.Route.Host)
		if rt.Host == host {
			normalized = rt
			break
		}
	}
	scheme := "https"
	if !cfg.TLS.Enabled {
		scheme = "http"
	}
	writeJSON(w, 200, map[string]any{"route": normalized, "expected": req.Expected, "access_mode": req.AccessMode,
		"url": scheme + "://" + normalized.Host + "/", "dns": "manual_not_verified",
		"tls":    tlsCoverage(cfg, normalized.Host),
		"checks": []string{"Open the trial hostname and check real content, assets, callbacks and uploads", "Test an allowed person and a person without access", "Block the old direct origin before claiming private access", "Keep the previous hosting and DNS values until final acceptance"}})
}

func (h *Handler) applicationInput(w http.ResponseWriter, r *http.Request) (applicationRequest, bool) {
	var req applicationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid application request")
		return req, false
	}
	if req.Expected == "" || req.Expected != h.manager.ConfigDigest() {
		writeError(w, 409, "configuration changed; refresh and review again")
		return req, false
	}
	if (req.AccessMode != "public" && req.AccessMode != "private") ||
		(req.AccessMode == "private" && req.Route.Access == nil && !req.Route.AuthRequired) ||
		(req.AccessMode == "public" && req.Route.RequiresAuth()) {
		writeError(w, 400, "choose public or private explicitly and provide a matching access policy")
		return req, false
	}
	if !h.allowOperatorRoot(w, r, req.Route, true) {
		return req, false
	}
	return req, true
}

func tlsCoverage(cfg config.Config, host string) string {
	if normalized, err := config.NormalizeHost(host); err == nil {
		host = normalized
	}
	if !cfg.TLS.Enabled {
		return "external_or_local_tls_not_verified"
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	for _, domain := range cfg.TLS.Domains {
		if certs.Covers(domain, name) {
			return "configured_coverage_certificate_not_probed"
		}
	}
	// HTTP-01: the listener orders a certificate for any configured route on
	// its first visit (routeHostPolicy), so only names a public CA would
	// refuse, or routes on another port, stay uncovered.
	if cfg.TLS.DNS01 == nil && certs.PublicName(name) && (name == host || strings.HasSuffix(host, ":443")) {
		return "automatic_on_first_visit"
	}
	return "not_covered"
}

func (h *Handler) saveApplication(w http.ResponseWriter, r *http.Request) {
	req, ok := h.applicationInput(w, r)
	if !ok {
		return
	}
	cfg := h.manager.Config()
	if _, err := config.WithApplication(cfg, req.Route); err != nil {
		writeError(w, 422, err.Error())
		return
	}
	if tlsCoverage(cfg, req.Route.Host) == "not_covered" {
		writeError(w, 422, "this edge cannot get a certificate for this hostname: use a name its certificate covers (tls.domains) or a public hostname")
		return
	}
	writer, ok := h.manager.(applicationWriter)
	if !ok {
		writeError(w, 503, "application editing is unavailable")
		return
	}
	if !h.recordMutationIntent(w, r, "application.save", safeRouteEventHost(req.Route.Host)) {
		return
	}
	if err := writer.PutApplication(req.Route, req.Expected); err != nil {
		status := 422
		if errors.Is(err, routemanager.ErrConfigChanged) {
			status = 409
		}
		writeError(w, status, "application could not be saved; refresh and review its configuration")
		return
	}
	h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Host: safeRouteEventHost(req.Route.Host), Code: "application.saved", ConfigSHA256: h.manager.ConfigDigest()})
	writeJSON(w, 200, map[string]string{"status": "saved", "config_sha256": h.manager.ConfigDigest()})
}

func (h *Handler) removeApplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expected string `json:"expected"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.Expected == "" {
		writeError(w, 400, "reviewed configuration digest required")
		return
	}
	if req.Expected != h.manager.ConfigDigest() {
		writeError(w, 409, "configuration changed; refresh and review again")
		return
	}
	host := r.PathValue("host")
	if _, err := config.WithoutApplication(h.manager.Config(), host); err != nil {
		writeError(w, 422, "cannot remove the last configured route or leave a project without domains; disconnect its publisher first if needed")
		return
	}
	writer, ok := h.manager.(interface{ RemoveApplication(string, string) error })
	if !ok {
		writeError(w, 503, "application removal is unavailable")
		return
	}
	if !h.recordMutationIntent(w, r, "application.remove", safeRouteEventHost(host)) {
		return
	}
	if err := writer.RemoveApplication(host, req.Expected); err != nil {
		writeError(w, 409, "application removal could not be confirmed; refresh before retrying")
		return
	}
	h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Host: safeRouteEventHost(host), Code: "application.removed", ConfigSHA256: h.manager.ConfigDigest()})
	writeJSON(w, 200, map[string]bool{"route_removed": true, "content_retained": true})
}
