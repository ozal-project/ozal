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
	"path"
	"sort"
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
	lifetime     auth.Lifetime
	allowedHosts map[string]bool
	// protected are the hosts behind the login, offered as explicit links
	// when a sign-in or enrolment has no destination to return to.
	protected  []string
	ceremonies *ceremonies
	sink       events.Sink
	now        func() time.Time
	// oidc is the upstream login offered beside passkeys, if any.
	oidc     OIDCLogin
	oidcName string
	// apps returns the current routes for the portal (ID-012).
	apps func() []config.Route
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
	var protected []string
	for _, h := range protectedHosts {
		host, err := config.NormalizeHost(h)
		if err != nil {
			return nil, fmt.Errorf("passkey: invalid protected host %q: %w", h, err)
		}
		hosts[host] = true
		if host != loginHost {
			protected = append(protected, login.Scheme+"://"+host+"/")
		}
	}
	sort.Strings(protected)
	return &Gate{
		webauthn:     wa,
		dir:          dir,
		sessions:     sessions,
		origin:       origin,
		loginHost:    loginHost,
		secure:       login.Scheme == "https",
		cookieDomain: cfg.CookieDomain,
		lifetime:     auth.SessionLifetime(cfg),
		allowedHosts: hosts,
		protected:    protected,
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
	if session.Principal.Method == auth.MethodOIDC {
		if g.oidc != nil && g.oidc.AcceptsPrincipal(session.Principal) {
			return session.Principal, true
		}
		return auth.Principal{}, false
	}
	id, native := strings.CutPrefix(session.Principal.Subject, SubjectPrefix)
	if !native || session.Principal.Method != auth.MethodPasskey {
		return auth.Principal{}, false
	}
	user, err := g.dir.ActiveUser(r.Context(), id)
	if err != nil {
		return auth.Principal{}, false
	}
	// Membership is authoritative on every request, including a login racing
	// a membership update. The session's cached groups never grant stale access.
	session.Principal.Groups = user.Groups
	session.Principal.Email = user.Email

	return session.Principal, true
}

// RedirectToLogin sends the browser to the login page on the login host,
// remembering where it was going. A visitor who still has a valid session is
// there for a step-up, and the page says so instead of explaining
// invitations; the flag only changes wording, never what the login checks.
func (g *Gate) RedirectToLogin(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || g.secure {
		scheme = "https"
	}
	returnTo := scheme + "://" + r.Host + r.URL.RequestURI()
	target := g.origin + "/_/auth/login?return_to=" + url.QueryEscape(returnTo)
	if _, ok := g.Authenticate(r); ok {
		target += "&reauth=1"
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// RedirectToStepUp is RedirectToLogin for a visitor the route wants to see
// log in again. A device-bound or method requirement is named so the page
// can offer the right way in; the names only change wording and which
// buttons show, never what the login checks.
func (g *Gate) RedirectToStepUp(w http.ResponseWriter, r *http.Request, code auth.DecisionCode, methods []string) {
	var reason string
	switch code {
	case auth.CodeDeviceBoundRequired:
		reason = "&reauth=device"
	case auth.CodeMethodNotAllowed:
		reason = "&reauth=method&methods=" + url.QueryEscape(strings.Join(methods, ","))
	case auth.CodeContextStepUpRequired:
		reason = "&reauth=method&methods=" + url.QueryEscape(auth.MethodPasskey)
	default:
		g.RedirectToLogin(w, r)
		return
	}
	scheme := "http"
	if r.TLS != nil || g.secure {
		scheme = "https"
	}
	returnTo := scheme + "://" + r.Host + r.URL.RequestURI()
	http.Redirect(w, r, g.origin+"/_/auth/login?return_to="+url.QueryEscape(returnTo)+reason, http.StatusFound)
}

// OIDCLogin is the upstream provider's login, offered next to passkeys
// (ID-007). auth.Gate satisfies it; the interface keeps this package from
// depending on how that login works.
type OIDCLogin interface {
	AcceptsPrincipal(auth.Principal) bool
	HandleLogin(w http.ResponseWriter, r *http.Request)
	HandleCallback(w http.ResponseWriter, r *http.Request)
}

// WithOIDC offers an upstream provider on the login page, labelled name.
// Its sessions share the store and the cookie; they carry Method "oidc".
func (g *Gate) WithOIDC(login OIDCLogin, name string) *Gate {
	g.oidc = login
	g.oidcName = name
	return g
}

// ServeAuthEndpoint handles the passkey pages and API under /_/auth/. It
// reports false for any other path, which then belongs to the route.
func (g *Gate) ServeAuthEndpoint(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/_/auth/oidc/start", "/_/auth/callback":
		if g.oidc == nil {
			return false
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return true
		}
		if r.URL.Path == "/_/auth/callback" {
			g.oidc.HandleCallback(w, r)
		} else {
			g.oidc.HandleLogin(w, r)
		}
	case "/_/auth/login", "/_/auth/enrol":
		g.servePage(w, r)
	case "/_/auth/devices":
		g.serveDevicesPage(w, r)
	case portalPath:
		g.serveMePage(w, r)
	case portalPath + "/sessions":
		g.api(w, r, g.sessionsList)
	case portalPath + "/sessions/revoke":
		g.api(w, r, g.sessionsRevoke)
	case portalPath + "/data":
		g.api(w, r, g.meData)
	case "/_/auth/devices/list":
		g.api(w, r, g.devicesList)
	case "/_/auth/devices/add/begin":
		g.api(w, r, g.devicesAddBegin)
	case "/_/auth/devices/add/finish":
		g.api(w, r, g.devicesAddFinish)
	case "/_/auth/devices/revoke":
		g.api(w, r, g.devicesRevoke)
	case "/_/auth/passkey.js", "/_/auth/passkey.css", "/_/auth/geist-400.woff2", "/_/auth/geist-600.woff2":
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
		if path.Dir(r.URL.Path) != "/_/auth/icons" {
			return false
		}
		serveAsset(w, r)
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
	errSignedOut     = apiError{http.StatusUnauthorized, "You are signed out. Sign in again."}
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
	if apiErr := g.startCeremony(w, ceremony{kind: kindLogin, session: *session, returnTo: g.destination(req.ReturnTo)}); apiErr != nil {
		return nil, apiErr
	}
	g.emit(r, events.Event{Type: events.LoginStarted, Code: "passkey"})
	return assertion, nil
}

type finishResponse struct {
	// Redirect is where the page should go next; empty means stay, say the
	// visitor is signed in and offer Sites as explicit links.
	Redirect string   `json:"redirect"`
	Sites    []string `json:"sites,omitempty"`
}

// destination validates a return address against the configured hosts. It
// returns "" (no automatic redirect) rather than a fallback path: the login
// host serves no site of its own, so the page offers explicit links instead.
func (g *Gate) destination(returnTo string) string {
	if returnTo == "" {
		return ""
	}
	if safe := auth.SafeReturnTo(g.allowedHosts, returnTo); safe != "/" {
		return safe
	}
	return ""
}

// finished says where a sign-in or enrolment goes next: the validated
// destination, or else the portal, where the person sees the applications
// they may open (ID-012). Sites stays for a page script that cannot redirect.
func (g *Gate) finished(returnTo string) finishResponse {
	if returnTo != "" {
		return finishResponse{Redirect: returnTo}
	}
	return finishResponse{Redirect: g.origin + portalPath, Sites: g.protected}
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
	firstSeen, err := g.dir.RecordLogin(r.Context(), *credential)
	if err != nil {
		// Revoked between lookup and now, or a storage failure: refuse.
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: subject, Code: "passkey_record_failed", Error: err.Error()})
		return nil, &errLoginFailed
	}
	if apiErr := g.startSession(w, r, found.user, !credential.Flags.BackupEligible, credential.Flags.UserVerified, firstSeen); apiErr != nil {
		return nil, apiErr
	}
	g.emit(r, events.Event{Type: events.LoginSucceeded, Actor: subject, Code: "passkey"})
	return g.finished(pending.returnTo), nil
}

// --- enrolment ----------------------------------------------------------

type enrolBeginRequest struct {
	Token string `json:"token"`
	// ReturnTo comes from the invitation link or from a login page visited
	// earlier in this browser; it is validated like a login's.
	ReturnTo string `json:"return_to"`
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
	if apiErr := g.startCeremony(w, ceremony{kind: kindEnrol, session: *session, userID: user.ID, tokenHash: tokenHash[:], returnTo: g.destination(req.ReturnTo)}); apiErr != nil {
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
	if apiErr := g.startSession(w, r, user, !credential.Flags.BackupEligible, credential.Flags.UserVerified, true); apiErr != nil {
		return nil, apiErr
	}
	return g.finished(pending.returnTo), nil
}

// --- sessions, cookies and ceremonies ------------------------------------

// startSession issues a new session ID (never reusing one that existed
// before the login) and ends the session the browser had, if any.
func (g *Gate) startSession(w http.ResponseWriter, r *http.Request, user User, deviceBound, userVerified, deviceFirstSeen bool) *apiError {
	if previous, err := r.Cookie(auth.SessionCookieName); err == nil {
		g.sessions.Delete(r.Context(), previous.Value)
	}
	firstSeenEvidence := deviceFirstSeen
	id, err := g.sessions.Create(r.Context(), auth.Principal{
		Subject: user.Subject(),
		Email:   user.Email,
		// The operator asserted this address when inviting the user.
		EmailVerified:   true,
		Groups:          user.Groups,
		AuthTime:        g.now(),
		Method:          auth.MethodPasskey,
		DeviceBound:     deviceBound,
		UserVerified:    &userVerified,
		DeviceFirstSeen: &firstSeenEvidence,
	})
	if errors.Is(err, auth.ErrStoreFull) {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "session_store_full"})
		return &errBusy
	}
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: user.Subject(), Code: "session_create_failed", Error: err.Error()})
		return &errInternal
	}
	http.SetCookie(w, auth.SessionCookie(id, g.cookieDomain, g.secure, g.lifetime))
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
