package passkey

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
)

const (
	// ceremonyCookie holds the ID of the pending ceremony. Path and
	// SameSite=Strict keep it on the login endpoints of this site only.
	ceremonyCookie = "sb_passkey"
	// maxJSONBody bounds every API request body; a WebAuthn response is a
	// few kilobytes.
	maxJSONBody = 64 << 10
)

// Gate authenticates visitors with passkeys. It implements the proxy's
// AuthGate (Authenticate, RedirectToLogin) and serves the /_/auth/ pages
// and API through ServeAuthEndpoint. Sessions use the same backend and
// cookie as the OIDC gate, so the rest of Switchboard does not change.
type Gate struct {
	webauthn     *webauthn.WebAuthn
	dir          Directory
	sessions     auth.SessionBackend
	origin       string // e.g. https://auth.example.com
	loginHost    string // normalized host of origin
	secure       bool
	cookieDomain string
	allowedHosts map[string]bool
	ceremonies   *ceremonies
	sink         events.Sink
	now          func() time.Time
}

// NewGate builds the gate from a validated auth config. protectedHosts are
// the hosts a post-login redirect may land on; the login host is added.
func NewGate(cfg config.Auth, protectedHosts []string, dir Directory, sessions auth.SessionBackend, sink events.Sink) (*Gate, error) {
	if cfg.Passkeys == nil {
		return nil, errors.New("passkey: auth.passkeys is not configured")
	}
	if dir == nil || sessions == nil {
		return nil, errors.New("passkey: NewGate requires a directory and a session store")
	}
	if sink == nil {
		sink = events.Discard
	}
	login, err := url.Parse(cfg.Passkeys.LoginURL)
	if err != nil {
		return nil, fmt.Errorf("passkey: login_url: %w", err)
	}
	loginHost, err := config.NormalizeHost(login.Host)
	if err != nil {
		return nil, fmt.Errorf("passkey: login_url host: %w", err)
	}
	origin := login.Scheme + "://" + login.Host
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.Passkeys.RPID,
		RPDisplayName: cfg.Passkeys.DisplayName,
		RPOrigins:     []string{origin},
		// Discoverable credentials (no username to type or enumerate) and
		// user verification (biometric or PIN on the device) always.
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
		// Synced passkeys are not attested, and Switchboard does not
		// restrict authenticator models in phase 1.
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: ceremonyTTL, TimeoutUVD: ceremonyTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: ceremonyTTL, TimeoutUVD: ceremonyTTL},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("passkey: %w", err)
	}
	hosts := map[string]bool{loginHost: true}
	for _, h := range protectedHosts {
		host, err := config.NormalizeHost(h)
		if err != nil {
			return nil, fmt.Errorf("passkey: invalid protected host %q: %w", h, err)
		}
		hosts[host] = true
	}
	return &Gate{
		webauthn:     wa,
		dir:          dir,
		sessions:     sessions,
		origin:       origin,
		loginHost:    loginHost,
		secure:       login.Scheme == "https",
		cookieDomain: cfg.CookieDomain,
		allowedHosts: hosts,
		ceremonies:   newCeremonies(time.Now),
		sink:         sink,
		now:          time.Now,
	}, nil
}

// Authenticate returns the principal of a valid session whose user is still
// enabled. The user check runs on every request, so disabling someone takes
// effect at once even for a session the database did not delete.
func (g *Gate) Authenticate(r *http.Request) (auth.Principal, bool) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		return auth.Principal{}, false
	}
	session, ok := g.sessions.Get(r.Context(), cookie.Value)
	if !ok {
		return auth.Principal{}, false
	}
	id, ok := strings.CutPrefix(session.Principal.Subject, SubjectPrefix)
	if !ok {
		return auth.Principal{}, false
	}
	if _, err := g.dir.ActiveUser(r.Context(), id); err != nil {
		return auth.Principal{}, false
	}
	return session.Principal, true
}

// RedirectToLogin sends the browser to the login page on the login host,
// remembering where it was going.
func (g *Gate) RedirectToLogin(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || g.secure {
		scheme = "https"
	}
	returnTo := scheme + "://" + r.Host + r.URL.RequestURI()
	http.Redirect(w, r, g.origin+"/_/auth/login?return_to="+url.QueryEscape(returnTo), http.StatusFound)
}

// ServeAuthEndpoint handles the passkey pages and API under /_/auth/. It
// reports false for any other path, which then belongs to the route.
func (g *Gate) ServeAuthEndpoint(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/_/auth/login", "/_/auth/enrol":
		g.servePage(w, r)
	case "/_/auth/passkey.js", "/_/auth/passkey.css":
		serveAsset(w, r)
	case "/_/auth/login/begin":
		g.api(w, r, g.loginBegin)
	case "/_/auth/login/finish":
		g.api(w, r, g.loginFinish)
	case "/_/auth/enrol/begin":
		g.api(w, r, g.enrolBegin)
	case "/_/auth/enrol/finish":
		g.api(w, r, g.enrolFinish)
	case "/_/auth/logout":
		g.logout(w, r)
	default:
		return false
	}
	return true
}

// apiError is what the browser sees: one generic message per situation.
// The precise reason goes to the event, never to the visitor.
type apiError struct {
	status  int
	message string
}

var (
	errLoginFailed   = apiError{http.StatusUnauthorized, "That passkey was not accepted. Try again, or ask the operator for help."}
	errLinkInvalid   = apiError{http.StatusBadRequest, "This link is invalid, already used or expired. Ask the operator for a new one."}
	errExpired       = apiError{http.StatusBadRequest, "The request expired. Reload the page and try again."}
	errBusy          = apiError{http.StatusServiceUnavailable, "Too many sign-ins at once. Try again shortly."}
	errInternal      = apiError{http.StatusInternalServerError, "Something went wrong on the server."}
	errBadRequest    = apiError{http.StatusBadRequest, "Bad request."}
	errWrongOrigin   = apiError{http.StatusForbidden, "Sign in from the login page."}
	errWrongMethod   = apiError{http.StatusMethodNotAllowed, "Method not allowed."}
	errWrongEncoding = apiError{http.StatusUnsupportedMediaType, "Send JSON."}
)

// api runs a JSON endpoint behind the checks every one of them needs:
// POST, a JSON body of bounded size, and an Origin equal to the login
// origin (a page on another site cannot drive the ceremony, and a browser
// always sends Origin on a cross-origin or POST fetch).
func (g *Gate) api(w http.ResponseWriter, r *http.Request, handle func(w http.ResponseWriter, r *http.Request) (any, *apiError)) {
	var (
		result any
		failed *apiError
	)
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case r.Method != http.MethodPost:
		w.Header().Set("Allow", http.MethodPost)
		failed = &errWrongMethod
	case r.Header.Get("Origin") != g.origin:
		failed = &errWrongOrigin
	case mediaType != "application/json":
		failed = &errWrongEncoding
	default:
		r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
		result, failed = handle(w, r)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if failed != nil {
		w.WriteHeader(failed.status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": failed.message})
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func decodeJSON(r *http.Request, into any) bool {
	body, err := io.ReadAll(r.Body)
	return err == nil && json.Unmarshal(body, into) == nil
}

// --- login --------------------------------------------------------------

type loginBeginRequest struct {
	ReturnTo    string `json:"return_to"`
	Conditional bool   `json:"conditional"`
}

func (g *Gate) loginBegin(w http.ResponseWriter, r *http.Request) (any, *apiError) {
	var req loginBeginRequest
	if !decodeJSON(r, &req) {
		return nil, &errBadRequest
	}
	mediation := protocol.MediationDefault
	if req.Conditional {
		mediation = protocol.MediationConditional
	}
	assertion, session, err := g.webauthn.BeginDiscoverableMediatedLogin(mediation, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Code: "passkey_begin_failed", Error: err.Error()})
		return nil, &errInternal
	}
	returnTo := ""
	if req.ReturnTo != "" {
		returnTo = auth.SafeReturnTo(g.allowedHosts, req.ReturnTo)
	}
	if apiErr := g.startCeremony(w, ceremony{kind: kindLogin, session: *session, returnTo: returnTo}); apiErr != nil {
		return nil, apiErr
	}
	g.emit(r, events.Event{Type: events.LoginStarted, Code: "passkey"})
	return assertion, nil
}

type finishResponse struct {
	// Redirect is where the page should go next; empty means stay and say
	// the visitor is signed in.
	Redirect string `json:"redirect"`
}

func (g *Gate) loginFinish(w http.ResponseWriter, r *http.Request) (any, *apiError) {
	pending, ok := g.takeCeremony(w, r, kindLogin)
	if !ok {
		g.emit(r, events.Event{Type: events.LoginFailed, Code: "ceremony_unknown"})
		return nil, &errExpired
	}
	var found *webauthnUser
	lookup := func(rawID, userHandle []byte) (webauthn.User, error) {
		user, credentials, err := g.dir.UserByHandle(r.Context(), userHandle)
		if err != nil {
			return nil, err
		}
		found = &webauthnUser{user: user, credentials: credentials}
		return found, nil
	}
	_, credential, err := g.webauthn.FinishPasskeyLogin(lookup, pending.session, r)
	if err != nil || found == nil {
		code := "passkey_invalid"
		if errors.Is(err, ErrNotFound) {
			code = "passkey_unknown"
		}
		g.emit(r, events.Event{Type: events.LoginFailed, Code: code, Error: errorText(err)})
		return nil, &errLoginFailed
	}
	subject := found.user.Subject()
	// A counter that went backwards means two copies of the private key
	// are in use. Synced passkeys report 0 and never trip this.
	if credential.Authenticator.CloneWarning {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: subject, Code: "passkey_clone_warning"})
		return nil, &errLoginFailed
	}
	if err := g.dir.RecordLogin(r.Context(), *credential); err != nil {
		// Revoked between lookup and now, or a storage failure: refuse.
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: subject, Code: "passkey_record_failed", Error: err.Error()})
		return nil, &errLoginFailed
	}
	if apiErr := g.startSession(w, r, found.user); apiErr != nil {
		return nil, apiErr
	}
	g.emit(r, events.Event{Type: events.LoginSucceeded, Actor: subject, Code: "passkey"})
	return finishResponse{Redirect: pending.returnTo}, nil
}

// --- enrolment ----------------------------------------------------------

type enrolBeginRequest struct {
	Token string `json:"token"`
}

func (g *Gate) enrolBegin(w http.ResponseWriter, r *http.Request) (any, *apiError) {
	var req enrolBeginRequest
	if !decodeJSON(r, &req) || req.Token == "" || len(req.Token) > 128 {
		return nil, &errLinkInvalid
	}
	tokenHash := sha256.Sum256([]byte(req.Token))
	user, err := g.dir.InvitedUser(r.Context(), tokenHash[:])
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Code: "invitation_invalid", Error: errorText(err)})
		return nil, &errLinkInvalid
	}
	// Existing passkeys are excluded so the same authenticator is not
	// registered twice for one user.
	_, existing, err := g.dir.UserByHandle(r.Context(), user.Handle)
	if err != nil && !errors.Is(err, ErrNotFound) {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "enrol_lookup_failed", Error: err.Error()})
		return nil, &errInternal
	}
	wu := &webauthnUser{user: user, credentials: existing}
	creation, session, err := g.webauthn.BeginRegistration(wu,
		webauthn.WithExclusions(webauthn.Credentials(existing).CredentialDescriptors()),
	)
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "enrol_begin_failed", Error: err.Error()})
		return nil, &errInternal
	}
	if apiErr := g.startCeremony(w, ceremony{kind: kindEnrol, session: *session, userID: user.ID, tokenHash: tokenHash[:]}); apiErr != nil {
		return nil, apiErr
	}
	return creation, nil
}

func (g *Gate) enrolFinish(w http.ResponseWriter, r *http.Request) (any, *apiError) {
	pending, ok := g.takeCeremony(w, r, kindEnrol)
	if !ok {
		g.emit(r, events.Event{Type: events.LoginFailed, Code: "ceremony_unknown"})
		return nil, &errExpired
	}
	user, err := g.dir.ActiveUser(r.Context(), pending.userID)
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Code: "enrol_user_unavailable", Error: err.Error()})
		return nil, &errLinkInvalid
	}
	credential, err := g.webauthn.FinishRegistration(&webauthnUser{user: user}, pending.session, r)
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "enrol_invalid", Error: errorText(err)})
		return nil, &errLoginFailed
	}
	if err := g.dir.Enrol(r.Context(), pending.tokenHash, user.ID, *credential); err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "enrol_store_failed", Error: err.Error()})
		if errors.Is(err, ErrInvitationInvalid) {
			return nil, &errLinkInvalid
		}
		return nil, &errInternal
	}
	kind := "device_bound"
	if credential.Flags.BackupEligible {
		kind = "synced"
	}
	// Alerted when notifications are configured: a passkey nobody expected
	// is the sign that a link or a session was stolen.
	g.emit(r, events.Event{Type: events.PasskeyRegistered, Actor: user.Subject(), Code: kind})
	if apiErr := g.startSession(w, r, user); apiErr != nil {
		return nil, apiErr
	}
	return finishResponse{}, nil
}

// --- sessions, cookies and ceremonies ------------------------------------

// startSession issues a new session ID (never reusing one that existed
// before the login) and ends the session the browser had, if any.
func (g *Gate) startSession(w http.ResponseWriter, r *http.Request, user User) *apiError {
	if previous, err := r.Cookie(auth.SessionCookieName); err == nil {
		g.sessions.Delete(r.Context(), previous.Value)
	}
	id, err := g.sessions.Create(r.Context(), auth.Principal{
		Subject: user.Subject(),
		Email:   user.Email,
		// The operator asserted this address when inviting the user.
		EmailVerified: true,
		Groups:        user.Groups,
	})
	if errors.Is(err, auth.ErrStoreFull) {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "session_store_full"})
		return &errBusy
	}
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "session_create_failed", Error: err.Error()})
		return &errInternal
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    id,
		Domain:   g.cookieDomain,
		Path:     "/",
		HttpOnly: true,
		Secure:   g.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (g *Gate) startCeremony(w http.ResponseWriter, c ceremony) *apiError {
	id, err := g.ceremonies.put(c)
	if errors.Is(err, errCeremoniesFull) {
		return &errBusy
	}
	if err != nil {
		return &errInternal
	}
	http.SetCookie(w, &http.Cookie{
		Name:     ceremonyCookie,
		Value:    id,
		Path:     "/_/auth/",
		MaxAge:   int(ceremonyTTL.Seconds()),
		HttpOnly: true,
		Secure:   g.secure,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

// takeCeremony consumes the browser's pending ceremony and clears its cookie.
func (g *Gate) takeCeremony(w http.ResponseWriter, r *http.Request, kind ceremonyKind) (ceremony, bool) {
	http.SetCookie(w, &http.Cookie{Name: ceremonyCookie, Value: "", Path: "/_/auth/", MaxAge: -1, HttpOnly: true, Secure: g.secure, SameSite: http.SameSiteStrictMode})
	cookie, err := r.Cookie(ceremonyCookie)
	if err != nil {
		return ceremony{}, false
	}
	return g.ceremonies.take(cookie.Value, kind)
}

func (g *Gate) logout(w http.ResponseWriter, r *http.Request) {
	// A GET would let any page log the user out with an image tag.
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		g.sessions.Delete(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: "", Domain: g.cookieDomain, Path: "/", MaxAge: -1, HttpOnly: true, Secure: g.secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, g.origin+"/_/auth/login", http.StatusSeeOther)
}

func (g *Gate) emit(r *http.Request, e events.Event) {
	e.Time = g.now()
	e.Host = r.Host
	e.RemoteAddr = r.RemoteAddr
	g.sink.Emit(e)
}

// errorText keeps protocol errors short in events; the library's details
// can be long and are for the operator only.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	var perr *protocol.Error
	if errors.As(err, &perr) && perr.DevInfo != "" {
		return perr.Details + ": " + perr.DevInfo
	}
	return err.Error()
}

// webauthnUser adapts a User and their credentials to the library.
type webauthnUser struct {
	user        User
	credentials []webauthn.Credential
}

func (u *webauthnUser) WebAuthnID() []byte   { return u.user.Handle }
func (u *webauthnUser) WebAuthnName() string { return u.user.Email }
func (u *webauthnUser) WebAuthnDisplayName() string {
	if u.user.Name != "" {
		return u.user.Name
	}
	return u.user.Email
}
func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }
