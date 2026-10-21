package passkey

import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/events"
)

// selfServiceMaxAge is how recent the passkey proof behind a session must be
// to add or revoke passkeys (ID-008). An old session alone, or a stolen
// cookie, is not enough: the visitor confirms with a passkey first.
const selfServiceMaxAge = 5 * time.Minute

var (
	errStale       = apiError{http.StatusForbidden, "Confirm it's you first: sign in with your passkey again."}
	errLastPasskey = apiError{http.StatusConflict, "This is your only passkey. Add another one before removing it."}
	errNoPasskey   = apiError{http.StatusNotFound, "That passkey is not one of yours, or it is already removed."}
)

// freshUser returns the passkey user behind a session whose last passkey
// proof is recent enough for self-service.
func (g *Gate) freshUser(r *http.Request) (User, bool) {
	principal, ok := g.Authenticate(r)
	if !ok || principal.Method != auth.MethodPasskey || g.now().Sub(principal.AuthTime) > selfServiceMaxAge {
		return User{}, false
	}
	id, ok := strings.CutPrefix(principal.Subject, SubjectPrefix)
	if !ok {
		return User{}, false
	}
	user, err := g.dir.ActiveUser(r.Context(), id)
	return user, err == nil
}

// serveDevicesPage serves the page only to a fresh passkey session; anyone
// else goes through the login page and comes back.
func (g *Gate) serveDevicesPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.freshUser(r); !ok && r.Method == http.MethodGet {
		target := g.origin + "/_/auth/login?return_to=" + url.QueryEscape(g.origin+"/_/auth/devices")
		if _, signedIn := g.Authenticate(r); signedIn {
			target += "&reauth=1&methods=passkey"
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	g.servePage(w, r)
}

type deviceView struct {
	ID       string    `json:"id"`
	Provider string    `json:"provider"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitzero"`
	Synced   bool      `json:"synced"`
}

func (g *Gate) devicesList(w http.ResponseWriter, r *http.Request) (any, *apiError) {
	user, ok := g.freshUser(r)
	if !ok {
		return nil, &errStale
	}
	keys, err := g.dir.ActivePasskeys(r.Context(), user.ID)
	if err != nil {
		return nil, &errInternal
	}
	views := make([]deviceView, 0, len(keys))
	for _, k := range keys {
		views = append(views, deviceView{
			ID:       hex.EncodeToString(k.Credential.ID),
			Provider: ProviderName(k.Credential.Authenticator.AAGUID, k.Synced()),
			Created:  k.CreatedAt,
			LastUsed: k.LastUsedAt,
			Synced:   k.Synced(),
		})
	}
	return map[string]any{"email": user.Email, "passkeys": views, "sites": g.protected}, nil
}

func (g *Gate) devicesAddBegin(w http.ResponseWriter, r *http.Request) (any, *apiError) {
	user, ok := g.freshUser(r)
	if !ok {
		return nil, &errStale
	}
	keys, err := g.dir.ActivePasskeys(r.Context(), user.ID)
	if err != nil {
		return nil, &errInternal
	}
	existing := make([]webauthn.Credential, 0, len(keys))
	for _, k := range keys {
		existing = append(existing, k.Credential)
	}
	// The same authenticator cannot be registered twice for one user.
	creation, session, err := g.webauthn.BeginRegistration(&webauthnUser{user: user, credentials: existing},
		webauthn.WithExclusions(webauthn.Credentials(existing).CredentialDescriptors()),
	)
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "add_begin_failed", Error: err.Error()})
		return nil, &errInternal
	}
	if apiErr := g.startCeremony(w, ceremony{kind: kindAddDevice, session: *session, userID: user.ID}); apiErr != nil {
		return nil, apiErr
	}
	return creation, nil
}

func (g *Gate) devicesAddFinish(w http.ResponseWriter, r *http.Request) (any, *apiError) {
	pending, ok := g.takeCeremony(w, r, kindAddDevice)
	if !ok {
		return nil, &errExpired
	}
	// The session must still be fresh and still belong to the user the
	// ceremony began for.
	user, ok := g.freshUser(r)
	if !ok || user.ID != pending.userID {
		return nil, &errStale
	}
	credential, err := g.webauthn.FinishRegistration(&webauthnUser{user: user}, pending.session, r)
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "add_invalid", Error: errorText(err)})
		return nil, &errLoginFailed
	}
	if err := g.dir.AddPasskey(r.Context(), user.ID, *credential); err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "add_store_failed", Error: err.Error()})
		return nil, &errInternal
	}
	kind := "device_bound"
	if credential.Flags.BackupEligible {
		kind = "synced"
	}
	// Alerted like an enrolment: a passkey nobody expected is the sign of a
	// stolen session.
	g.emit(r, events.Event{Type: events.PasskeyRegistered, Actor: user.Subject(), Code: kind, Reason: "added from a signed-in session"})
	return map[string]string{"id": hex.EncodeToString(credential.ID), "provider": ProviderName(credential.Authenticator.AAGUID, credential.Flags.BackupEligible)}, nil
}

type revokeRequest struct {
	ID string `json:"id"`
}

func (g *Gate) devicesRevoke(w http.ResponseWriter, r *http.Request) (any, *apiError) {
	user, ok := g.freshUser(r)
	if !ok {
		return nil, &errStale
	}
	var req revokeRequest
	if !decodeJSON(r, &req) {
		return nil, &errBadRequest
	}
	id, err := hex.DecodeString(req.ID)
	if err != nil || len(id) == 0 || len(id) > 1023 {
		return nil, &errBadRequest
	}
	switch err := g.dir.RevokeOwnPasskey(r.Context(), user.ID, id); {
	case errors.Is(err, ErrLastPasskey):
		return nil, &errLastPasskey
	case errors.Is(err, ErrNotFound):
		return nil, &errNoPasskey
	case err != nil:
		return nil, &errInternal
	}
	g.emit(r, events.Event{Type: events.PasskeyRevoked, Actor: user.Subject(), Reason: "revoked from a signed-in session"})
	// The revocation ended every session of the user, this one included.
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: "", Domain: g.cookieDomain, Path: "/", MaxAge: -1, HttpOnly: true, Secure: g.secure, SameSite: http.SameSiteLaxMode})
	return map[string]bool{"signed_out": true}, nil
}
