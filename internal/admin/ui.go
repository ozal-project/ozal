package admin

import (
	"embed"
	"fmt"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"html/template"
	"net/http"
)

//go:embed ui/*
var consoleAssets embed.FS

var dashboardTemplate = template.Must(template.ParseFS(consoleAssets, "ui/status.html", "ui/nav.html"))
var activityTemplate = template.Must(template.ParseFS(consoleAssets, "ui/activity.html", "ui/nav.html"))

func (h *Handler) consoleCSS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	asset, _ := consoleAssets.ReadFile("ui/console.css")
	_, _ = w.Write(asset)
}

type accessRoute struct {
	Host      string `json:"host"`
	Protected bool   `json:"protected"`
	Kind      string `json:"kind"`
	Policy    string `json:"policy"`
}

// overview exposes an explicit projection, never the raw/resolved config.
func (h *Handler) overview(w http.ResponseWriter, _ *http.Request) {
	cfg := h.manager.Config()
	routes := make([]accessRoute, 0, len(cfg.Routes))
	for _, route := range cfg.Routes {
		kind := "HTTP proxy"
		if route.IsStatic() {
			kind = "Static files"
		}
		routes = append(routes, accessRoute{Host: route.Host, Protected: route.RequiresAuth(), Kind: kind, Policy: policyLabel(route)})
	}
	writeJSON(w, http.StatusOK, struct {
		Routes []accessRoute `json:"routes"`
	}{routes})
}

func policyLabel(route config.Route) string {
	if !route.RequiresAuth() {
		return "No login required"
	}
	if route.Access == nil || (len(route.Access.AllowedEmails)+len(route.Access.AllowedDomains)+len(route.Access.RequiredGroups) == 0) {
		return "Any authenticated account"
	}
	p := route.Access
	return fmt.Sprintf("%d emails / %d domains / %d required groups", len(p.AllowedEmails), len(p.AllowedDomains), len(p.RequiredGroups))
}

func (h *Handler) consoleFont(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "font/ttf")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	asset, _ := consoleAssets.ReadFile("ui/fonts/manrope.ttf")
	_, _ = w.Write(asset)
}
