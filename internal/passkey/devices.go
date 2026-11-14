package passkey

import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/useragent"
)

// selfServiceMaxAge is how recent the passkey proof behind a session must be
// to add or revoke passkeys (ID-008). An old session alone, or a stolen
// cookie, is not enough: the visitor confirms with a passkey first.
const selfServiceMaxAge = 5 * time.Minute

var (
	errStale       = apiError{http.StatusForbidden, "Confirm it's you first: sign in with your passkey again.", nil}
	errLastPasskey = apiError{http.StatusConflict, "This is your only passkey. Add another one before removing it.", nil}
	errNoPasskey   = apiError{http.StatusNotFound, "That passkey is not one of yours, or it is already removed.", nil}
	errBadLabel    = apiError{http.StatusBadRequest, "Use 1 to 60 characters for the name, without line breaks.", nil}
)

// maxLabel is the longest passkey name the store accepts.
const maxLabel = 60

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
	ID       string `json:"id"`
	Provider string `json:"provider"`
	// ProviderKnown says Provider names where the passkey lives, rather than
	// a kind of passkey.
	ProviderKnown bool `json:"provider_known"`
	// Label is the owner's name for it; the page falls back to Provider.
	Label string `json:"label,omitempty"`
	// Current marks the passkey that signed this session in, and LastClient
	// is the browser of its most recent session that is still open.
	Current    bool      `json:"current"`
	LastClient string    `json:"last_client,omitempty"`
	Created    time.Time `json:"created"`
	LastUsed   time.Time `json:"last_used,omitzero"`
	Synced     bool      `json:"synced"`
	Icon       string    `json:"icon"`
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
	principal, _ := g.Authenticate(r)
	lastClient := g.lastClients(r, principal.Subject)
	views := make([]deviceView, 0, len(keys))
	for _, k := range keys {
		name, icon, known := Provider(k.Credential.Authenticator.AAGUID, k.Synced())
		id := hex.EncodeToString(k.Credential.ID)
		views = append(views, deviceView{
			ID:            id,
			Provider:      name,
			ProviderKnown: known,
			Label:         k.Label,
			Current:       principal.Passkey == id,
			LastClient:    lastClient[id],
			Icon:          icon,
			Created:       k.CreatedAt,
			LastUsed:      k.LastUsedAt,
			Synced:        k.Synced(),
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
	if err := g.dir.AddPasskey(r.Context(), user.ID, *credential, useragent.Label(r.UserAgent())); err != nil {
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
	return createdView(*credential), nil
}

// lastClients maps each passkey to the browser of the newest open session
// it signed in. The session list is newest first.
func (g *Gate) lastClients(r *http.Request, subject string) map[string]string {
	clients := map[string]string{}
	store, ok := g.sessions.(auth.SessionControl)
	if !ok {
		return clients
	}
	sessions, err := store.ListOwned(r.Context(), subject)
	if err != nil {
		return clients
	}
	for _, s := range sessions {
		if _, seen := clients[s.Passkey]; !seen && s.Passkey != "" && s.Client != "" {
			clients[s.Passkey] = s.Client
		}
	}
	return clients
}

type renameRequest struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// devicesRename names one of the visitor's passkeys. It needs the same
// fresh passkey proof as the rest of the page; the name is the owner's
// hint, shown only to them and to the operator.
func (g *Gate) devicesRename(w http.ResponseWriter, r *http.Request) (any, *apiError) {
	user, ok := g.freshUser(r)
	if !ok {
		return nil, &errStale
	}
	var req renameRequest
	if !decodeJSON(r, &req) {
		return nil, &errBadRequest
	}
	id, err := hex.DecodeString(req.ID)
	if err != nil || len(id) == 0 || len(id) > 1023 {
		return nil, &errBadRequest
	}
	label := strings.TrimSpace(req.Label)
	if label == "" || utf8.RuneCountInString(label) > maxLabel || len(label) > 4*maxLabel || strings.ContainsFunc(label, unicode.IsControl) {
		return nil, &errBadLabel
	}
	switch err := g.dir.RenamePasskey(r.Context(), user.ID, id, label); {
	case errors.Is(err, ErrNotFound):
		return nil, &errNoPasskey
	case err != nil:
		return nil, &errInternal
	}
	g.emit(r, events.Event{Type: events.Type("passkey.renamed"), Actor: user.Subject(), Reason: "renamed from a signed-in session"})
	return map[string]string{"id": req.ID, "label": label}, nil
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
	remaining, err := g.dir.RevokeOwnPasskey(r.Context(), user.ID, id)
	switch {
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
	// The passkeys left, read in the revocation's own transaction: the page
	// can tell the browser right away, though this session just ended.
	answer := map[string]any{"signed_out": true}
	if list := accepted(g.webauthn.Config.RPID, user.Handle, remaining, nil); list != nil {
		answer["accepted"] = list
	}
	return answer, nil
}

// createdPasskey tells the page that just created a passkey where it was
// saved, when the provider says so, and its id for naming it.
type createdPasskey struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	ProviderKnown bool   `json:"provider_known"`
	Icon          string `json:"icon"`
}

func createdView(credential webauthn.Credential) createdPasskey {
	name, icon, known := Provider(credential.Authenticator.AAGUID, credential.Flags.BackupEligible)
	return createdPasskey{ID: hex.EncodeToString(credential.ID), Provider: name, ProviderKnown: known, Icon: icon}
}
