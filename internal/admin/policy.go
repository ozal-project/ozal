package admin

import (
	"errors"
	"net/http"
	"slices"

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
)

type explainRequest struct {
	Host          string `json:"host"`
	RequestMethod string `json:"request_method,omitempty"`
	// Principal nil simulates a request without a session (Gate level).
	Principal *explainPrincipal `json:"principal"`
}

type explainPrincipal struct {
	Email           string   `json:"email"`
	EmailVerified   bool     `json:"email_verified"`
	Groups          []string `json:"groups"`
	Method          string   `json:"method,omitempty"`
	DeviceBound     *bool    `json:"device_bound,omitempty"`
	UserVerified    *bool    `json:"user_verified,omitempty"`
	DeviceFirstSeen *bool    `json:"device_first_seen,omitempty"`
}

// explainPolicy answers the UI-002 policy simulator with the same report as
// `ozal policy explain -json`. It needs the admin bearer token: the
// answer reveals allowlist contents and, used repeatedly, works as an oracle
// for who is allowed, which is more than the token-less console shows.
func (h *Handler) explainPolicy(w http.ResponseWriter, r *http.Request) {
	var req explainRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.RequestMethod != "" && !config.IsSupportedHTTPMethod(req.RequestMethod) {
		writeError(w, http.StatusBadRequest, "request_method is not a supported HTTP method")
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
		if req.Principal.Method != "" && !slices.Contains(h.manager.Config().Auth.LoginMethods(), req.Principal.Method) {
			writeError(w, http.StatusBadRequest, "principal.method is not a configured login method")
			return
		}
		if req.Principal.DeviceBound != nil && req.Principal.Method != auth.MethodPasskey {
			writeError(w, http.StatusBadRequest, "principal.device_bound requires principal.method=passkey")
			return
		}
		if req.Principal.UserVerified != nil && req.Principal.Method != auth.MethodPasskey {
			writeError(w, http.StatusBadRequest, "principal.user_verified requires principal.method=passkey")
			return
		}
		if req.Principal.DeviceFirstSeen != nil && req.Principal.Method != auth.MethodPasskey {
			writeError(w, http.StatusBadRequest, "principal.device_first_seen requires principal.method=passkey")
			return
		}
		principal = &auth.Principal{
			Email:           req.Principal.Email,
			EmailVerified:   req.Principal.EmailVerified,
			Groups:          req.Principal.Groups,
			Method:          req.Principal.Method,
			DeviceBound:     req.Principal.DeviceBound != nil && *req.Principal.DeviceBound,
			UserVerified:    req.Principal.UserVerified,
			DeviceFirstSeen: req.Principal.DeviceFirstSeen,
		}
	}
	writeJSON(w, http.StatusOK, auth.NewReportWithRequestMethod(host, route, principal, req.RequestMethod))
}

func (h *Handler) findRoute(host string) (config.Route, error) {
	for _, r := range h.manager.Config().Routes {
		if normalized, err := config.NormalizeHost(r.Host); err == nil && normalized == host {
			return r, nil
		}
	}
	return config.Route{}, errors.New("no route for host " + host)
}
