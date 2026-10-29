package admin

import (
	"embed"
	"fmt"
	"github.com/ozal-project/ozal/internal/config"
	"html/template"
	"net/http"
	"strings"
)

//go:embed ui/*
var consoleAssets embed.FS

var dashboardTemplate = template.Must(template.ParseFS(consoleAssets, "ui/status.html", "ui/nav.html"))

func (h *Handler) consoleCSS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	asset, _ := consoleAssets.ReadFile("ui/console.css")
	_, _ = w.Write(asset)
}

type accessRoute struct {
	Host       string `json:"host"`
	Protected  bool   `json:"protected"`
	Kind       string `json:"kind"`
	Policy     string `json:"policy"`
	Connector  string `json:"connector,omitempty"`
	Connection string `json:"connection,omitempty"`
}

// overview exposes an explicit projection, never the raw/resolved config.
func (h *Handler) overview(w http.ResponseWriter, _ *http.Request) {
	cfg := h.manager.Config()
	statuses := h.manager.RouteStatuses()
	connections := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		connections[status.Route.Host] = status.ConnectorConnected
	}
	routes := make([]accessRoute, 0, len(cfg.Routes))
	for _, route := range cfg.Routes {
		kind := "HTTP proxy"
		if route.IsStatic() {
			kind = "Static files"
		}
		view := accessRoute{Host: route.Host, Protected: route.RequiresAuth(), Kind: kind, Policy: policyLabel(route)}
		if route.Connector != "" {
			view.Kind, view.Connector, view.Connection = "Tunnel connector", route.Connector, "Connector offline"
			if connections[route.Host] {
				view.Connection = "Connector connected"
			}
		}
		routes = append(routes, view)
	}
	writeJSON(w, http.StatusOK, struct {
		Routes []accessRoute `json:"routes"`
	}{routes})
}

// policyLabel says, in a line, who a route lets in. This projection needs no
// token, so it states how many addresses, domains and groups the policy lists
// and never which ones: the names would tell anyone on the loopback who may
// sign in.
func policyLabel(route config.Route) string {
	if !route.RequiresAuth() {
		return "No login required"
	}
	if route.Access == nil {
		return "Any signed-in account"
	}
	p := route.Access
	var parts []string
	if n := len(p.AllowedEmails); n > 0 {
		parts = append(parts, count(n, "email"))
	}
	if n := len(p.AllowedDomains); n > 0 {
		parts = append(parts, count(n, "domain"))
	}
	label := "Any signed-in account"
	if len(parts) > 0 {
		label = "Allowlist: " + strings.Join(parts, ", ")
	}
	if n := len(p.RequiredGroups); n > 0 {
		label += " · needs " + count(n, "group")
	}
	if len(p.Methods) > 0 {
		label += " · login by " + strings.Join(p.Methods, " or ")
	}
	if p.RequireDeviceBound {
		label += " · passkey bound to the device"
	}
	if p.MaxAuthAgeMinutes > 0 {
		label += fmt.Sprintf(" · login within %d min", p.MaxAuthAgeMinutes)
	}
	if p.ContextRule != nil {
		label += " · context rule"
	}
	return label
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (h *Handler) consoleFont(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "font/ttf")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	asset, _ := consoleAssets.ReadFile("ui/fonts/manrope.ttf")
	_, _ = w.Write(asset)
}
