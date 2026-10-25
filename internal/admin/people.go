package admin

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/passkey"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

// people is the user and passkey store as the console needs it
// (sqlite.Identity implements it): the same operations as `switchboard
// user`, so the operator never needs a shell for them.
type people interface {
	Users(ctx context.Context) ([]passkey.User, error)
	Passkeys(ctx context.Context, email string) ([]passkey.Passkey, error)
	Invite(ctx context.Context, email, name string, groups []string, recoverAccess bool, ttl time.Duration) (sqlite.Invitation, error)
	RevokePasskey(ctx context.Context, email, prefix string) error
	SetDisabled(ctx context.Context, email string, disabled bool) error
}

// WithPeople enables the people endpoints (ID-003 operations in the console).
func WithPeople(store people) Option {
	return func(h *Handler) { h.people = store }
}

func (h *Handler) registerPeople() {
	h.mux.HandleFunc("GET /_/api/people", h.listPeople)
	h.mux.HandleFunc("POST /_/api/people/invite", h.requireBearer(h.invitePerson))
	h.mux.HandleFunc("POST /_/api/people/{email}/disable", h.requireBearer(h.setPersonDisabled(true)))
	h.mux.HandleFunc("POST /_/api/people/{email}/enable", h.requireBearer(h.setPersonDisabled(false)))
	h.mux.HandleFunc("POST /_/api/people/{email}/passkeys/{id}/revoke", h.requireBearer(h.revokePersonPasskey))
}

type personPasskey struct {
	ID       string    `json:"id"`
	Provider string    `json:"provider"`
	Synced   bool      `json:"synced"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitzero"`
	Revoked  time.Time `json:"revoked,omitzero"`
}

type person struct {
	Email    string          `json:"email"`
	Subject  string          `json:"subject"`
	Name     string          `json:"name,omitempty"`
	Groups   []string        `json:"groups"`
	Created  time.Time       `json:"created"`
	Disabled time.Time       `json:"disabled,omitzero"`
	Passkeys []personPasskey `json:"passkeys"`
}

// listPeople reads like the rest of the console: the admin listener is
// loopback-only, and the activity feed already shows these identities.
func (h *Handler) listPeople(w http.ResponseWriter, r *http.Request) {
	cfg := h.manager.Config()
	users, err := h.people.Users(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "users could not be listed")
		return
	}
	out := make([]person, 0, len(users))
	for _, u := range users {
		keys, err := h.people.Passkeys(r.Context(), u.Email)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "passkeys could not be listed")
			return
		}
		p := person{Email: u.Email, Subject: u.Subject(), Name: u.Name, Groups: u.Groups, Created: u.CreatedAt, Disabled: u.DisabledAt, Passkeys: []personPasskey{}}
		if p.Groups == nil {
			p.Groups = []string{}
		}
		for _, k := range keys {
			p.Passkeys = append(p.Passkeys, personPasskey{
				ID:       hex.EncodeToString(k.Credential.ID),
				Provider: passkey.ProviderName(k.Credential.Authenticator.AAGUID, k.Synced()),
				Synced:   k.Synced(),
				Created:  k.CreatedAt,
				LastUsed: k.LastUsedAt,
				Revoked:  k.RevokedAt,
			})
		}
		out = append(out, p)
	}
	var sites []string
	for _, rt := range cfg.Routes {
		if rt.RequiresAuth() {
			sites = append(sites, rt.Host)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": cfg.Auth.Passkeys != nil, "users": out, "protected_hosts": sites})
}

// maxInviteMinutes caps an enrolment link at a day, as the CLI always has.
const maxInviteMinutes = 24 * 60

type inviteRequest struct {
	Email      string   `json:"email"`
	Name       string   `json:"name"`
	Groups     []string `json:"groups"`
	Recover    bool     `json:"recover"`
	ReturnTo   string   `json:"return_to"`
	TTLMinutes int      `json:"ttl_minutes"`
}

// invitePerson issues a one-time enrolment link. The link carries the
// token, so it is returned only behind the admin token and never logged.
func (h *Handler) invitePerson(w http.ResponseWriter, r *http.Request) {
	var req inviteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Recovery adds a passkey to an account that already exists: the path an
	// account takeover would take. It stays with the host's shell, the root of
	// trust (identity-login.md): the admin unix socket, never the admin
	// bearer, which also travels to the machines that publish sites.
	if req.Recover && !isLocal(r) {
		writeError(w, http.StatusForbidden, "account recovery runs on the host only: switchboard user invite -recover")
		return
	}
	if req.TTLMinutes < 0 || req.TTLMinutes > maxInviteMinutes {
		writeError(w, http.StatusBadRequest, "ttl_minutes must be between 1 and 1440 (0 keeps the 15-minute default)")
		return
	}
	cfg := h.manager.Config()
	// Check the destination before creating anything.
	if _, err := passkey.InviteLink(cfg, "", strings.TrimSpace(req.ReturnTo)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ttl := 15 * time.Minute
	if req.TTLMinutes > 0 {
		ttl = time.Duration(req.TTLMinutes) * time.Minute
	}
	var groups []string
	for _, g := range req.Groups {
		if g = strings.TrimSpace(g); g != "" {
			groups = append(groups, g)
		}
	}
	if !h.recordMutationIntent(w, r, "user.invite", req.Email) {
		return
	}
	invitation, err := h.people.Invite(r.Context(), req.Email, strings.TrimSpace(req.Name), groups, req.Recover, ttl)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	link, err := passkey.InviteLink(cfg, invitation.Token, strings.TrimSpace(req.ReturnTo))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	code := "invite"
	if req.Recover {
		code = "recover"
	}
	h.emit(events.Event{Type: events.UserInvited, Actor: actor(r), Host: invitation.User.Email, Code: code})
	writeJSON(w, http.StatusCreated, map[string]any{"email": invitation.User.Email, "link": link, "expires_at": invitation.ExpiresAt.UTC()})
}

func (h *Handler) setPersonDisabled(disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := r.PathValue("email")
		code := "user.enable"
		if disabled {
			code = "user.disable"
		}
		if !h.recordMutationIntent(w, r, code, email) {
			return
		}
		if err := h.people.SetDisabled(r.Context(), email, disabled); err != nil {
			h.personError(w, err)
			return
		}
		kind := events.UserEnabled
		if disabled {
			kind = events.UserDisabled
		}
		h.emit(events.Event{Type: kind, Actor: actor(r), Host: email})
		writeJSON(w, http.StatusOK, map[string]any{"email": email, "disabled": disabled})
	}
}

func (h *Handler) revokePersonPasskey(w http.ResponseWriter, r *http.Request) {
	email, id := r.PathValue("email"), r.PathValue("id")
	if !h.recordMutationIntent(w, r, "passkey.revoke", email) {
		return
	}
	if err := h.people.RevokePasskey(r.Context(), email, id); err != nil {
		h.personError(w, err)
		return
	}
	h.emit(events.Event{Type: events.PasskeyRevoked, Actor: actor(r), Host: email, Reason: "revoked by the operator"})
	writeJSON(w, http.StatusOK, map[string]any{"email": email, "revoked": id})
}

func (h *Handler) personError(w http.ResponseWriter, err error) {
	if errors.Is(err, passkey.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such user")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}
