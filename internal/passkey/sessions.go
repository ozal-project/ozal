package passkey

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/events"
)

func (g *Gate) sessionsList(_ http.ResponseWriter, r *http.Request) (any, *apiError) {
	principal, ok := g.Authenticate(r)
	if !ok {
		return nil, &errSignedOut
	}
	store, ok := g.sessions.(auth.SessionControl)
	if !ok {
		return nil, &errInternal
	}
	sessions, err := store.ListOwned(r.Context(), principal.Subject)
	if err != nil {
		return nil, &errInternal
	}
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		return nil, &errSignedOut
	}
	current := sha256.Sum256([]byte(cookie.Value))
	currentID := hex.EncodeToString(current[:])
	for i := range sessions {
		sessions[i].Current = sessions[i].ID == currentID
	}
	return map[string]any{"sessions": sessions}, nil
}

func (g *Gate) sessionsRevoke(w http.ResponseWriter, r *http.Request) (any, *apiError) {
	principal, ok := g.Authenticate(r)
	if !ok {
		return nil, &errSignedOut
	}
	var req struct {
		ID  string `json:"id"`
		All bool   `json:"all"`
	}
	if !decodeJSON(r, &req) {
		return nil, &errBadRequest
	}
	if (req.ID == "" && !req.All) || (req.ID != "" && req.All) {
		return nil, &errBadRequest
	}
	store, ok := g.sessions.(auth.SessionControl)
	if !ok {
		return nil, &errInternal
	}
	if err := store.RevokeOwned(r.Context(), principal.Subject, req.ID, req.All); err != nil {
		if errors.Is(err, auth.ErrSessionNotFound) {
			missing := apiError{404, "That session is not yours or has already ended."}
			return nil, &missing
		}
		return nil, &errInternal
	}
	cookie, _ := r.Cookie(auth.SessionCookieName)
	sum := sha256.Sum256([]byte(cookie.Value))
	endedCurrent := req.All || req.ID == hex.EncodeToString(sum[:])
	if endedCurrent {
		http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: "", Path: "/", Domain: g.cookieDomain, MaxAge: -1, HttpOnly: true, Secure: g.secure, SameSite: http.SameSiteLaxMode})
	}
	g.emit(r, events.Event{Type: events.Type("session.revoked"), Actor: principal.Subject, Code: "self_service", Host: g.origin})
	return map[string]bool{"signed_out": endedCurrent}, nil
}
