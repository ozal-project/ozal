package admin

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/adminid"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/useragent"
)

// Access requests. An administrator can turn on a form on the sign-in page
// where a person with no account asks to become an administrator. Asking
// grants nothing: the request waits in memory until a signed-in administrator
// who holds the administrators tool approves it with a fresh passkey and picks
// the tools, or denies it. An approval produces the same one-use invitation an
// administrator would send by hand, but it is handed only to the browser that
// asked (see adminid.Requests). The switch is off until someone turns it on.

// accessRequestsEnabled reads the switch; any failure counts as off.
func (h *Handler) accessRequestsEnabled(r *http.Request) bool {
	on, err := h.identities.AccessRequests(r.Context())
	return err == nil && on
}

func (h *Handler) requestAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !sameOrigin(r) {
		writeError(w, http.StatusBadRequest, "open the console on its own origin")
		return
	}
	if !h.accessRequestsEnabled(r) {
		writeErrorCode(w, http.StatusForbidden, "this console is not taking access requests; ask an administrator or the host", "requests_off")
		return
	}
	var req struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "give your full name and a valid email")
		return
	}
	name, ok := cleanName(req.Name)
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !ok || !validEmail(email) {
		writeError(w, http.StatusBadRequest, "give your full name and a valid email")
		return
	}
	// An email that already is an administrator gets the same answer as any
	// other, so this public form cannot be used to learn who is one.
	known := false
	if accounts, err := h.identities.Accounts(r.Context()); err == nil {
		for _, a := range accounts {
			if a.Email == email {
				known = true
			}
		}
	} else {
		writeError(w, http.StatusServiceUnavailable, "the request could not be saved")
		return
	}
	id, secret, expires, err := h.requests.Submit(name, email, useragent.Label(r.UserAgent()), known)
	if errors.Is(err, adminid.ErrRequestBusy) {
		writeError(w, http.StatusTooManyRequests, "too many requests are waiting; try again later")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the request could not be saved")
		return
	}
	code := "requested"
	if known {
		code = "known_administrator"
	}
	h.emit(events.Event{Type: events.Type("admin.access_requested"), Actor: "browser", Host: email, Code: code})
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "secret": secret, "expires": expires})
}

// accessRequestStatus lets the requester's browser ask how it went. Only the
// holder of the secret can; an approval hands over its link exactly once.
func (h *Handler) accessRequestStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !sameOrigin(r) {
		writeError(w, http.StatusBadRequest, "open the console on its own origin")
		return
	}
	var req struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if err := decodeJSON(w, r, &req); err != nil || len(req.ID) > 32 || len(req.Secret) > 128 {
		writeError(w, http.StatusBadRequest, "this request is not valid")
		return
	}
	state, token, err := h.requests.Poll(req.ID, req.Secret)
	if err != nil {
		writeErrorCode(w, http.StatusNotFound, "this request expired or was already answered; ask again if you still need access", "request_gone")
		return
	}
	body := map[string]string{"state": string(state)}
	if token != "" {
		body["token"] = token
	}
	writeJSON(w, http.StatusOK, body)
}

type accessRequestView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Browser string `json:"browser,omitempty"`
	Created string `json:"created_at"`
	Expires string `json:"expires_at"`
}

func (h *Handler) listAccessRequests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	pending := h.requests.Pending()
	out := make([]accessRequestView, 0, len(pending))
	for _, p := range pending {
		out = append(out, accessRequestView{ID: p.ID, Name: p.Name, Email: p.Email, Browser: p.Label, Created: p.Created.UTC().Format(time.RFC3339), Expires: p.Expires.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": h.accessRequestsEnabled(r), "requests": out})
}

func (h *Handler) setAccessRequests(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "send enabled as true or false")
		return
	}
	target := "off"
	if req.Enabled {
		target = "on"
	}
	if !h.recordMutationIntent(w, r, "admin.access_requests", target) {
		return
	}
	h.identityMu.Lock()
	defer h.identityMu.Unlock()
	if err := h.identities.SetAccessRequests(r.Context(), req.Enabled); err != nil {
		writeError(w, http.StatusServiceUnavailable, "the change could not be saved")
		return
	}
	if !req.Enabled {
		// Turning it off also ends what is waiting, so nothing approved later
		// answers a form that no longer exists.
		h.requests.Clear()
	}
	h.emit(events.Event{Type: events.Type("admin.access_requests_" + target), Actor: h.actorOf(r)})
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": req.Enabled})
}

// approveAccessRequest turns a request into a one-use invitation for the
// browser that asked. It needs a signed-in administrator account with a fresh
// passkey proof and the administrators tool, and it cannot hand out a tool the
// approver's own session lacks.
func (h *Handler) approveAccessRequest(w http.ResponseWriter, r *http.Request) {
	grant, _ := r.Context().Value(operatorKey{}).(operatorGrant)
	if !h.operators.passkeyFresh(grant.ID, passkeyStepUpMaxAge) {
		writeErrorCode(w, http.StatusForbidden, "confirm this approval with your passkey", "passkey_required")
		return
	}
	var req struct {
		Tools []string `json:"tools"`
	}
	if err := decodeJSON(w, r, &req); err != nil || len(req.Tools) == 0 {
		writeError(w, http.StatusBadRequest, "choose at least one tool; known tools: "+strings.Join(operatorTools, ", "))
		return
	}
	tools, err := normalizeTools(req.Tools)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, tool := range tools {
		if !grant.allows(tool) {
			writeError(w, http.StatusForbidden, "you cannot grant the "+tool+" tool: this session does not include it")
			return
		}
	}
	h.identityMu.Lock()
	defer h.identityMu.Unlock()
	pending, err := h.requests.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "that request expired or was already answered")
		return
	}
	if !h.recordMutationIntent(w, r, "admin.access_approve", pending.Email) {
		return
	}
	token, hash, err := newEnrolmentToken()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the invitation could not be issued")
		return
	}
	expires := time.Now().Add(adminid.ApprovedTTL)
	switch err := h.identities.IssueEnrolment(r.Context(), adminid.EnrolmentRequest{Kind: enrolInvite, Email: pending.Email, Name: pending.Name, Tools: tools}, hash, expires); {
	case errors.Is(err, adminid.ErrEmailTaken):
		writeError(w, http.StatusConflict, "that email already is an administrator")
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "the invitation could not be issued")
		return
	}
	if err := h.requests.Approve(pending.ID, token); err != nil {
		writeError(w, http.StatusNotFound, "that request expired or was already answered")
		return
	}
	h.emit(events.Event{Type: events.Type("admin.access_approved"), Actor: h.actorOf(r), Host: pending.Email, Reason: "tools: " + strings.Join(tools, ",")})
	writeJSON(w, http.StatusOK, map[string]any{"approved": true, "email": pending.Email, "tools": tools})
}

func (h *Handler) denyAccessRequest(w http.ResponseWriter, r *http.Request) {
	h.identityMu.Lock()
	defer h.identityMu.Unlock()
	pending, err := h.requests.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "that request expired or was already answered")
		return
	}
	if !h.recordMutationIntent(w, r, "admin.access_deny", pending.Email) {
		return
	}
	if err := h.requests.Deny(pending.ID); err != nil {
		writeError(w, http.StatusNotFound, "that request expired or was already answered")
		return
	}
	h.emit(events.Event{Type: events.Type("admin.access_denied"), Actor: h.actorOf(r), Host: pending.Email})
	writeJSON(w, http.StatusOK, map[string]bool{"denied": true})
}
