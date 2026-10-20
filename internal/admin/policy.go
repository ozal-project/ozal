package admin

import (
	"errors"
	"net/http"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
)

type explainRequest struct {
	Host string `json:"host"`
	// Principal nil simulates a request without a session (Gate level).
	Principal *explainPrincipal `json:"principal"`
}

type explainPrincipal struct {
	Email         string   `json:"email"`
	EmailVerified bool     `json:"email_verified"`
	Groups        []string `json:"groups"`
}

// explainPolicy answers the UI-002 policy simulator with the same report as
// `switchboard policy explain -json`. It needs the admin bearer token: the
// answer reveals allowlist contents and, used repeatedly, works as an oracle
// for who is allowed, which is more than the token-less console shows.
func (h *Handler) explainPolicy(w http.ResponseWriter, r *http.Request) {
	var req explainRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	host, err := config.NormalizeHost(req.Host)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid host: "+err.Error())
		return
	}
	route, err := h.findRoute(host)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var principal *auth.Principal
	if req.Principal != nil {
		if req.Principal.Email == "" {
			writeError(w, http.StatusBadRequest, "principal.email is required; send principal: null to simulate no session")
			return
		}
		principal = &auth.Principal{
			Email:         req.Principal.Email,
			EmailVerified: req.Principal.EmailVerified,
			Groups:        req.Principal.Groups,
		}
	}
	writeJSON(w, http.StatusOK, auth.NewReport(host, route, principal))
}

func (h *Handler) findRoute(host string) (config.Route, error) {
	for _, r := range h.manager.Config().Routes {
		if normalized, err := config.NormalizeHost(r.Host); err == nil && normalized == host {
			return r, nil
		}
	}
	return config.Route{}, errors.New("no route for host " + host)
}
