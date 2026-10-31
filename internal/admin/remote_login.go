package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"errors"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ozal-project/ozal/internal/adminid"
	"github.com/ozal-project/ozal/internal/events"
)

// Administrator passkeys on the HTTPS login host (ID-021). Many authenticators
// refuse http://ozal.localhost (Bitwarden offers no passkeys on localhost), so
// with admin.login_host the ceremony runs on auth.passkeys.login_url while the
// console stays on loopback:
//
//  1. The console tab keeps a random verifier V and asks its own edge, over
//     loopback, for a transaction bound to SHA-256(V) and to its origin.
//  2. The browser opens the login host with that transaction; the page there
//     runs the usual sign-in or enrolment against the administrator directory.
//  3. The login host answers with a one-use code in the fragment of a URL on
//     the console's origin, and the tab trades the code and V for a session.
//
// The code is worthless without V, which never leaves the tab that started.
// Without a transaction created over loopback the login host serves nothing
// here: every request falls through as an unknown path would.

const (
	remoteTxTTL   = 10 * time.Minute
	remoteCodeTTL = 60 * time.Second
	remoteLimit   = 64
	remotePath    = "/_/auth/admin"
)

//go:embed loginhost
var loginHostAssets embed.FS

// RemoteLogin holds the transactions and codes of login-host ceremonies. It is
// shared by the loopback console (which starts and redeems them) and the
// login host's gate (which runs the ceremony), so it lives in memory of the
// one process both run in; a restart simply ends them.
type RemoteLogin struct {
	handler atomic.Pointer[Handler]
	mu      sync.Mutex
	now     func() time.Time
	txs     map[string]remoteTx
	codes   map[[32]byte]remoteCode
}

type remoteTx struct {
	challenge [32]byte // SHA-256 of the console tab's verifier
	origin    string   // the console origin that started: the only return
	enrol     *enrolRequest
	hints     []string
	expires   time.Time
}

type remoteCode struct {
	challenge    [32]byte
	origin       string
	handle       []byte
	credentialID []byte
	expires      time.Time
}

// NewRemoteLogin returns an empty book; WithRemoteLogin connects it to the
// console's handler.
func NewRemoteLogin() *RemoteLogin {
	return &RemoteLogin{now: time.Now, txs: map[string]remoteTx{}, codes: map[[32]byte]remoteCode{}}
}

// WithRemoteLogin lets administrators use passkeys on the login host when the
// config sets admin.login_host. The same RemoteLogin goes to the login gate.
func WithRemoteLogin(remote *RemoteLogin) Option {
	return func(h *Handler) {
		h.remote = remote
		remote.handler.Store(h)
	}
}

func randomID() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (rl *RemoteLogin) sweep(now time.Time) {
	for id, tx := range rl.txs {
		if !now.Before(tx.expires) {
			delete(rl.txs, id)
		}
	}
	for key, c := range rl.codes {
		if !now.Before(c.expires) {
			delete(rl.codes, key)
		}
	}
}

func (rl *RemoteLogin) start(tx remoteTx) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	rl.sweep(now)
	if len(rl.txs) >= remoteLimit {
		return "", errors.New("too many sign-ins in progress")
	}
	tx.expires = now.Add(remoteTxTTL)
	rl.txs[id] = tx
	return id, nil
}

// lookup returns a live transaction without using it.
func (rl *RemoteLogin) lookup(id string) (remoteTx, bool) {
	if id == "" || len(id) > 64 {
		return remoteTx{}, false
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	tx, ok := rl.txs[id]
	if !ok || !rl.now().Before(tx.expires) {
		return remoteTx{}, false
	}
	return tx, true
}

// issue ends a transaction with a one-use code for the console.
func (rl *RemoteLogin) issue(id string, account adminid.Account, credentialID []byte) (code, origin string, err error) {
	code, err = randomID()
	if err != nil {
		return "", "", err
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	tx, ok := rl.txs[id]
	delete(rl.txs, id)
	if !ok || !now.Before(tx.expires) {
		return "", "", errors.New("transaction expired")
	}
	rl.sweep(now)
	if len(rl.codes) >= remoteLimit {
		return "", "", errors.New("too many sign-ins in progress")
	}
	rl.codes[sha256.Sum256([]byte(code))] = remoteCode{
		challenge: tx.challenge, origin: tx.origin, handle: account.Handle,
		credentialID: credentialID, expires: now.Add(remoteCodeTTL),
	}
	return code, tx.origin, nil
}

// redeem uses a code up, whatever the outcome, and returns who it is for when
// the verifier and the origin are the ones that started its transaction.
func (rl *RemoteLogin) redeem(code, verifier, origin string) (remoteCode, bool) {
	key := sha256.Sum256([]byte(code))
	rl.mu.Lock()
	c, ok := rl.codes[key]
	delete(rl.codes, key)
	now := rl.now()
	rl.mu.Unlock()
	proof := sha256.Sum256([]byte(verifier))
	if !ok || !now.Before(c.expires) || verifier == "" || c.origin != origin ||
		subtle.ConstantTimeCompare(proof[:], c.challenge[:]) != 1 {
		return remoteCode{}, false
	}
	return c, true
}

// loginSite is the relying party of the login host when admin.login_host is
// on, and that host's origin.
func (h *Handler) loginSite() (*webauthn.WebAuthn, string, bool) {
	if h.remote == nil || h.identities == nil {
		return nil, "", false
	}
	cfg := h.manager.Config()
	if !cfg.Admin.LoginHost || cfg.Auth.Passkeys == nil {
		return nil, "", false
	}
	login, err := url.Parse(cfg.Auth.Passkeys.LoginURL)
	if err != nil || login.Host == "" {
		return nil, "", false
	}
	origin := login.Scheme + "://" + login.Host
	rp, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.Auth.Passkeys.RPID,
		RPDisplayName: "Ozal console",
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: 5 * time.Minute, TimeoutUVD: 5 * time.Minute},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: 5 * time.Minute, TimeoutUVD: 5 * time.Minute},
		},
	})
	if err != nil {
		return nil, "", false
	}
	return rp, origin, true
}

// consoleOrigin is the private console's origin when r comes from its own
// page on a loopback name, the only place a login-host ceremony may return to.
func consoleOriginOf(r *http.Request) (string, bool) {
	if !sameOrigin(r) || r.TLS != nil {
		return "", false
	}
	hostname, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		hostname = r.Host
	}
	switch strings.ToLower(hostname) {
	case "ozal.localhost", "switchboard.localhost", "localhost":
		return r.Header.Get("Origin"), true
	}
	return "", false
}

var allowedHints = map[string]bool{"client-device": true, "hybrid": true, "security-key": true}

// remoteStart is the console tab asking for a login-host ceremony. It holds the
// verifier; the edge keeps only its hash. An enrolment link travels inside the
// transaction and never in a URL of the login host.
func (h *Handler) remoteStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	origin, ok := consoleOriginOf(r)
	if !ok {
		writeError(w, http.StatusForbidden, "start from the private console")
		return
	}
	_, loginOrigin, ok := h.loginSite()
	if !ok {
		writeError(w, http.StatusNotFound, "passkeys on the login host are not turned on (admin.login_host)")
		return
	}
	var req struct {
		Challenge string   `json:"challenge"`
		Hints     []string `json:"hints"`
		enrolRequest
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	challenge, err := base64.RawURLEncoding.DecodeString(req.Challenge)
	if err != nil || len(challenge) != sha256.Size || len(req.Token) > 128 || len(req.Hints) > 3 {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	tx := remoteTx{challenge: [32]byte(challenge), origin: origin}
	if req.Token != "" {
		enrol := req.enrolRequest
		tx.enrol = &enrol
	}
	for _, hint := range req.Hints {
		if allowedHints[hint] {
			tx.hints = append(tx.hints, hint)
		}
	}
	id, err := h.remote.start(tx)
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "too many sign-ins in progress; try again shortly")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": loginOrigin + remotePath + "?tx=" + id})
}

// remoteFinish trades the code the login host returned, and the verifier only
// this tab holds, for the usual console session.
func (h *Handler) remoteFinish(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	origin, ok := consoleOriginOf(r)
	if !ok || h.remote == nil {
		writeError(w, http.StatusForbidden, "finish in the private console")
		return
	}
	var req struct {
		Code     string `json:"code"`
		Verifier string `json:"verifier"`
	}
	if err := decodeJSON(w, r, &req); err != nil || len(req.Code) > 64 || len(req.Verifier) > 128 {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	code, ok := h.remote.redeem(req.Code, req.Verifier, origin)
	if !ok {
		h.failedSignIn(r, "login_host_code_invalid", nil)
		writeError(w, http.StatusUnauthorized, "this sign-in expired or belongs to another tab; start again")
		return
	}
	h.identityMu.Lock()
	defer h.identityMu.Unlock()
	account, _, err := h.identities.AccountByHandle(r.Context(), code.handle)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "that administrator is no longer active")
		return
	}
	h.startAdminSession(w, r, account, code.credentialID)
}

// ServeAdminLogin answers the login host's /_/auth/admin paths. It returns
// false, so the request continues as any unknown path would, unless the
// feature is on and the request names a live transaction.
func (rl *RemoteLogin) ServeAdminLogin(w http.ResponseWriter, r *http.Request) bool {
	h := rl.handler.Load()
	if h == nil || (r.URL.Path != remotePath && r.URL.Path != remotePath+".js" && !strings.HasPrefix(r.URL.Path, remotePath+"/")) {
		return false
	}
	rp, loginOrigin, ok := h.loginSite()
	if !ok {
		return false
	}
	id := r.URL.Query().Get("tx")
	tx, ok := rl.lookup(id)
	if !ok {
		return false
	}
	loginHeaders(w.Header())
	post := r.Method == http.MethodPost
	if post && r.Header.Get("Origin") != loginOrigin {
		writeError(w, http.StatusForbidden, "open this page from its own address")
		return true
	}
	switch {
	case r.URL.Path == remotePath && !post:
		rl.servePage(w, tx, id)
	case r.URL.Path == remotePath+".js" && !post:
		body, _ := loginHostAssets.ReadFile("loginhost/admin-login.js")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(body)
	case r.URL.Path == remotePath+"/begin" && post:
		if tx.enrol != nil {
			h.beginEnrol(w, r, rp, *tx.enrol, id)
		} else {
			h.beginLogin(w, r, rp, id)
		}
	case r.URL.Path == remotePath+"/finish" && post:
		pending, ok := h.ceremonies.take(r.Header.Get(adminCeremony), tx.enrol != nil, loginOrigin)
		if !ok || pending.tx != id {
			h.failedSignIn(r, "ceremony_unknown", nil)
			writeError(w, http.StatusUnauthorized, "this step expired; start again from the console")
			return true
		}
		if tx.enrol != nil {
			h.finishEnrol(w, r, rp, pending, rl.handBack(id))
		} else {
			h.finishLogin(w, r, rp, pending, rl.handBack(id))
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
	return true
}

// handBack ends a verified ceremony on the login host: instead of a session,
// the browser is sent back to the console with a one-use code in the fragment,
// which no server, log or Referer sees.
func (rl *RemoteLogin) handBack(id string) ceremonyDone {
	return func(w http.ResponseWriter, r *http.Request, account adminid.Account, credentialID []byte) {
		code, origin, err := rl.issue(id, account, credentialID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "this sign-in expired; start again from the console")
			return
		}
		if h := rl.handler.Load(); h != nil {
			h.emit(events.Event{Type: events.Type("admin.login_host_ceremony"), Actor: "admin:" + account.Email})
		}
		writeJSON(w, http.StatusOK, map[string]string{"redirect": origin + "/overview#admin_code=" + url.QueryEscape(code)})
	}
}

func (rl *RemoteLogin) servePage(w http.ResponseWriter, tx remoteTx, id string) {
	body, err := loginHostAssets.ReadFile("loginhost/admin-login.html")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	kind := "sign-in"
	if tx.enrol != nil {
		kind = "create"
	}
	page := strings.NewReplacer(
		"{{tx}}", html.EscapeString(id),
		"{{kind}}", kind,
		"{{hints}}", html.EscapeString(strings.Join(tx.hints, " ")),
	).Replace(string(body))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

// loginHeaders are the login host's page headers (internal/passkey pageHeaders).
func loginHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; connect-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cache-Control", "no-store")
}
