package admin

import (
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ozal-project/ozal/internal/adminid"
	"github.com/ozal-project/ozal/internal/events"
)

// stepUpBegin starts a passkey assertion for the signed-in administrator.
// It never opens a new session and never accepts the admin token or the host
// socket: only a session already bound to a passkey account can be confirmed.
func (h *Handler) stepUpBegin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	grant, ok := r.Context().Value(operatorKey{}).(operatorGrant)
	if !ok {
		writeError(w, http.StatusForbidden, "sign in with an administrator passkey first")
		return
	}
	if h.identities == nil {
		writeError(w, http.StatusServiceUnavailable, "administrator passkeys are not configured on this host")
		return
	}
	rp, err := h.relyingParty(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	assertion, session, err := rp.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		h.failedSignIn(r, "step_up_begin_failed", err)
		writeError(w, http.StatusInternalServerError, "the passkey confirmation could not start")
		return
	}
	id, err := h.ceremonies.put(pendingCeremony{stepUp: grant.ID, origin: r.Header.Get("Origin"), session: *session})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "too many passkey prompts in progress; try again shortly")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ceremony": id, "options": assertion})
}

// stepUpFinish verifies the assertion and marks this session as confirmed for
// passkeyStepUpMaxAge. It refuses a passkey that belongs to another account.
func (h *Handler) stepUpFinish(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	grant, ok := r.Context().Value(operatorKey{}).(operatorGrant)
	if !ok {
		writeError(w, http.StatusForbidden, "sign in with an administrator passkey first")
		return
	}
	if h.identities == nil {
		writeError(w, http.StatusServiceUnavailable, "administrator passkeys are not configured on this host")
		return
	}
	rp, err := h.relyingParty(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pending, ok := h.ceremonies.take(r.Header.Get(adminCeremony), false, r.Header.Get("Origin"))
	if !ok || pending.tx != "" || pending.stepUp == "" || pending.stepUp != grant.ID {
		h.failedSignIn(r, "step_up_unknown", nil)
		writeError(w, http.StatusUnauthorized, "this passkey confirmation expired; try again")
		return
	}
	sessionAccount := h.operators.account(grant.ID)
	if sessionAccount == "" {
		writeError(w, http.StatusForbidden, "this session did not start with an administrator passkey; sign in again")
		return
	}
	h.finishLogin(w, r, rp, pending, func(w http.ResponseWriter, r *http.Request, account adminid.Account, credentialID []byte) {
		if account.ID != sessionAccount {
			writeError(w, http.StatusForbidden, "that passkey belongs to another administrator; use one for this session")
			return
		}
		now := h.operators.now()
		if !h.operators.markPasskey(grant.ID) {
			writeError(w, http.StatusUnauthorized, "the session ended before the passkey was confirmed")
			return
		}
		h.emit(events.Event{Type: events.Type("operator.step_up"), Actor: "admin:" + account.Email, Code: grant.ID})
		writeJSON(w, http.StatusOK, map[string]any{
			"verified":    true,
			"verified_at": now.UTC().Format(time.RFC3339),
			"expires_at":  now.Add(passkeyStepUpMaxAge).UTC().Format(time.RFC3339),
		})
	})
}
