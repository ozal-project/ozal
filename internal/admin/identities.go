package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ozal-project/ozal/internal/adminid"
	"github.com/ozal-project/ozal/internal/events"
)

// Administrative identity (UI-007): the people who run the console sign in
// with their own passkey instead of a command on the host. The host still
// opens the door once (`console enrol`) and is the only recovery path.
// Administrators live apart from application users, so an application
// passkey or group never opens the console.

const (
	enrolBootstrap = "bootstrap"
	enrolRecover   = "recover"
	enrolInvite    = "invite"
	enrolmentTTL   = 10 * time.Minute
	adminCeremony  = "X-Ozal-Ceremony"
)

// identities is what the sign-in flows need from storage; the SQLite adapter
// implements it.
type identities interface {
	Enrolled(ctx context.Context) (bool, error)
	// IssueEnrolment stores the hash of a host-issued one-use token. A
	// bootstrap fails with adminid.ErrAlreadyEnrolled once an administrator exists;
	// a recover needs the email of an existing one.
	IssueEnrolment(ctx context.Context, request adminid.EnrolmentRequest, tokenHash []byte, expires time.Time) error
	Enrolment(ctx context.Context, tokenHash []byte) (adminid.Enrolment, error)
	// CompleteEnrolment uses the token and stores the credential in one
	// transaction. A bootstrap creates the first account (all tools); a
	// recover revokes the account's previous passkeys.
	CompleteEnrolment(ctx context.Context, tokenHash []byte, account adminid.Account, credential webauthn.Credential, label string) (adminid.Account, error)
	// AccountByHandle returns an enabled administrator and active passkeys.
	AccountByHandle(ctx context.Context, handle []byte) (adminid.Account, []webauthn.Credential, error)
	// Accounts lists every administrator, enabled or not.
	Accounts(ctx context.Context) ([]adminid.Summary, error)
	// SetDisabled disables or enables one; disabling the last enabled
	// administrator who can manage administrators fails.
	SetDisabled(ctx context.Context, id string, disabled bool) (adminid.Account, error)
	// AddPasskey attaches another credential to an enabled administrator.
	AddPasskey(ctx context.Context, accountID string, credential webauthn.Credential, label string) error
	// Passkeys lists an administrator's active passkeys.
	Passkeys(ctx context.Context, accountID string) ([]adminid.Passkey, error)
	// RemovePasskey revokes one; the account's last passkey is refused.
	RemovePasskey(ctx context.Context, accountID string, credentialID []byte) error
	// RecordLogin stores a credential's new counter; it fails for a revoked one.
	RecordLogin(ctx context.Context, credential webauthn.Credential) error
}

// WithIdentities enables passkey sign-in for administrators.
func WithIdentities(store identities) Option {
	return func(h *Handler) { h.identities = store }
}

type pendingCeremony struct {
	enrol     bool
	kind      string
	origin    string
	session   webauthn.SessionData
	tokenHash []byte
	account   adminid.Account
	expires   time.Time
}

// deviceTTL is how long an "add this browser" link lasts.
const deviceTTL = 10 * time.Minute

// deviceBook holds one-use links that let a signed-in administrator add a
// passkey from another browser or password manager (a passkey saved in one
// manager is invisible to another browser). They live in memory only: a
// restart simply ends them.
type deviceBook struct {
	mu    sync.Mutex
	now   func() time.Time
	links map[[32]byte]deviceLink
}

type deviceLink struct {
	account string
	expires time.Time
}

func newDeviceBook() *deviceBook {
	return &deviceBook{now: time.Now, links: map[[32]byte]deviceLink{}}
}

func (b *deviceBook) issue(account string) (string, time.Time, error) {
	token, hash, err := newEnrolmentToken()
	if err != nil {
		return "", time.Time{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	for key, l := range b.links {
		if !now.Before(l.expires) || l.account == account {
			delete(b.links, key) // one live link per account
		}
	}
	if len(b.links) >= 64 {
		return "", time.Time{}, errors.New("too many device links")
	}
	expires := now.Add(deviceTTL)
	b.links[[32]byte(hash)] = deviceLink{account: account, expires: expires}
	return token, expires, nil
}

// peek returns the account a live link is for without using it.
func (b *deviceBook) peek(token string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	l, ok := b.links[sha256.Sum256([]byte(token))]
	if !ok || !b.now().Before(l.expires) {
		return "", false
	}
	return l.account, true
}

// take uses a link up; it succeeds once.
func (b *deviceBook) take(hash []byte, account string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := [32]byte(hash)
	l, ok := b.links[key]
	delete(b.links, key)
	return ok && l.account == account && b.now().Before(l.expires)
}

// revokeAccount ends every link of an account (recovered or disabled).
func (b *deviceBook) revokeAccount(account string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, l := range b.links {
		if l.account == account {
			delete(b.links, key)
		}
	}
}

// ceremonyBook keeps begun ceremonies in memory; each finishes at most once.
type ceremonyBook struct {
	mu      sync.Mutex
	now     func() time.Time
	pending map[string]pendingCeremony
}

func newCeremonyBook() *ceremonyBook {
	return &ceremonyBook{now: time.Now, pending: map[string]pendingCeremony{}}
}

func (b *ceremonyBook) put(c pendingCeremony) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	for key, p := range b.pending {
		if !now.Before(p.expires) {
			delete(b.pending, key)
		}
	}
	if len(b.pending) >= 64 {
		return "", errors.New("too many pending sign-ins")
	}
	c.expires = now.Add(5 * time.Minute)
	b.pending[id] = c
	return id, nil
}

func (b *ceremonyBook) take(id string, enrol bool, origin string) (pendingCeremony, bool) {
	b.mu.Lock()
	c, ok := b.pending[id]
	delete(b.pending, id)
	b.mu.Unlock()
	if !ok || c.enrol != enrol || c.origin != origin || !b.now().Before(c.expires) {
		return pendingCeremony{}, false
	}
	return c, true
}

type webUser struct {
	account     adminid.Account
	credentials []webauthn.Credential
}

func (u *webUser) WebAuthnID() []byte                         { return u.account.Handle }
func (u *webUser) WebAuthnName() string                       { return u.account.Email }
func (u *webUser) WebAuthnDisplayName() string                { return u.account.Name }
func (u *webUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func (h *Handler) registerIdentities() {
	h.mux.HandleFunc("GET /_/api/admin/state", h.adminState)
	h.mux.HandleFunc("POST /_/api/admin/devices", h.requireBearer(h.issueDeviceLink))
	h.mux.HandleFunc("POST /_/api/admin/passkeys/begin", h.requireBearer(h.addPasskeyBegin))
	h.mux.HandleFunc("POST /_/api/admin/passkeys/finish", h.requireBearer(h.addPasskeyFinish))
	h.mux.HandleFunc("GET /_/api/admin/passkeys", h.requireBearer(h.listPasskeys))
	h.mux.HandleFunc("DELETE /_/api/admin/passkeys/{id}", h.requireBearer(h.removePasskey))
	h.mux.HandleFunc("POST /_/api/admin/enrolments", h.requireLocal(h.issueEnrolment))
	h.mux.HandleFunc("POST /_/api/admin/enrol/info", h.enrolInfo)
	h.mux.HandleFunc("POST /_/api/admin/enrol/begin", h.enrolBegin)
	h.mux.HandleFunc("POST /_/api/admin/enrol/finish", h.enrolFinish)
	h.mux.HandleFunc("POST /_/api/admin/login/begin", h.adminLoginBegin)
	h.mux.HandleFunc("POST /_/api/admin/login/finish", h.adminLoginFinish)
	h.mux.HandleFunc("GET /_/api/admins", h.requireTool("administrators", h.listAdmins))
	h.mux.HandleFunc("POST /_/api/admins/invite", h.requireTool("administrators", h.inviteAdmin))
	h.mux.HandleFunc("POST /_/api/admins/{id}/recover", h.requireTool("administrators", h.recoverAdmin))
	h.mux.HandleFunc("POST /_/api/admins/{id}/disable", h.requireTool("administrators", h.setAdminDisabled(true)))
	h.mux.HandleFunc("POST /_/api/admins/{id}/enable", h.requireTool("administrators", h.setAdminDisabled(false)))
}

// relyingParty derives the WebAuthn relying party from the page's own origin.
// A passkey is bound to a registrable name, never to an IP address, so the
// console uses ozal.localhost (the SSH tunnel's local end). Legacy origins
// remain available with their own RP IDs to enroll on the new name.
func (h *Handler) relyingParty(r *http.Request) (*webauthn.WebAuthn, error) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	origin := scheme + "://" + r.Host
	if r.Header.Get("Origin") != origin {
		return nil, errors.New("open the console on its own origin")
	}
	hostname, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		hostname = r.Host
	}
	hostname = strings.ToLower(hostname)
	if hostname != "ozal.localhost" && hostname != "switchboard.localhost" && hostname != "localhost" {
		return nil, errors.New("passkeys need the console opened as ozal.localhost (for example http://ozal.localhost:9091), not by IP address")
	}
	return webauthn.New(&webauthn.Config{
		RPID:          hostname,
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
}

// WithEnrolCommand sets the exact command that creates the first administrator
// on this host, so the console shows one that works here instead of a generic
// guess. It is shown only while nobody is enrolled.
func WithEnrolCommand(command string) Option {
	return func(h *Handler) { h.enrolCommand = command }
}

func (h *Handler) adminState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	enrolled, err := h.identities.Enrolled(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "administrator directory unavailable")
		return
	}
	state := map[string]any{"enrolled": enrolled}
	if !enrolled && h.enrolCommand != "" {
		state["enrol_command"] = h.enrolCommand
	}
	writeJSON(w, http.StatusOK, state)
}

// issueEnrolment is the host's one door: a printed link, valid ten minutes.
func (h *Handler) issueEnrolment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind  string `json:"kind"`
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &req); err != nil || (req.Kind != enrolBootstrap && req.Kind != enrolRecover) {
		writeError(w, http.StatusBadRequest, "kind must be bootstrap or recover")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if req.Kind == enrolRecover && !validEmail(email) {
		writeError(w, http.StatusBadRequest, "recover needs the administrator's email")
		return
	}
	if !h.recordMutationIntent(w, r, "admin.enrolment", req.Kind) {
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusServiceUnavailable, "enrolment could not be issued")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	expires := time.Now().Add(enrolmentTTL)
	switch err := h.identities.IssueEnrolment(r.Context(), adminid.EnrolmentRequest{Kind: req.Kind, Email: email}, hash[:], expires); {
	case errors.Is(err, adminid.ErrAlreadyEnrolled):
		writeError(w, http.StatusConflict, "an administrator already exists; use recover with their email")
		return
	case errors.Is(err, adminid.ErrAccountNotFound):
		writeError(w, http.StatusNotFound, "no administrator has that email")
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "enrolment could not be issued")
		return
	}
	h.emit(events.Event{Type: events.Type("admin.enrolment_issued"), Actor: "local", Code: req.Kind})
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expires": expires})
}

// newEnrolmentToken makes a one-use token and the hash that is stored.
func newEnrolmentToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

// FirstRunTTL is how long the setup link printed at start stays valid.
const FirstRunTTL = 15 * time.Minute

// IssueFirstRun mints the one-use setup link for the first administrator,
// for the host to keep where only the host reads it (the installer's output
// and a root-only file; never the log). It returns "" when an administrator already exists.
// It is the same authority as `console enrol`: whoever can read the host's
// log can already run that command.
func IssueFirstRun(ctx context.Context, store identities) (string, time.Time, error) {
	enrolled, err := store.Enrolled(ctx)
	if err != nil || enrolled {
		return "", time.Time{}, err
	}
	token, hash, err := newEnrolmentToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(FirstRunTTL)
	if err := store.IssueEnrolment(ctx, adminid.EnrolmentRequest{Kind: enrolBootstrap}, hash, expires); err != nil {
		if errors.Is(err, adminid.ErrAlreadyEnrolled) {
			return "", time.Time{}, nil
		}
		return "", time.Time{}, err
	}
	return token, expires, nil
}

func validEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

func cleanName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	return name, name != "" && len(name) <= 80 && !strings.ContainsFunc(name, unicode.IsControl)
}

func (h *Handler) failedSignIn(r *http.Request, code string, err error) {
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	h.emit(events.Event{Type: events.Type("admin.sign_in_failed"), Actor: "browser", Code: code, Reason: reason})
}

// enrolInfo tells the holder of a valid link what the link is for, so the form
// asks only what the host does not already know: an invitation fixed the email,
// a recovery fixes the whole account. The link is the credential, and this
// answer does not use it up.
func (h *Handler) enrolInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.Token == "" || len(req.Token) > 128 {
		writeError(w, http.StatusBadRequest, "this enrolment link is not valid")
		return
	}
	if id, ok := h.devices.peek(req.Token); ok {
		account, _, found := h.accountWithCredentials(r.Context(), id)
		if !found {
			writeError(w, http.StatusUnauthorized, "this link expired or was already used; ask for a new one")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"kind": "device", "email": account.Email, "name": account.Name})
		return
	}
	hash := sha256.Sum256([]byte(req.Token))
	enrolment, err := h.identities.Enrolment(r.Context(), hash[:])
	if err != nil {
		h.failedSignIn(r, "enrolment_invalid", err)
		writeError(w, http.StatusUnauthorized, "this enrolment link expired or was already used; ask for a new one")
		return
	}
	info := map[string]string{"kind": enrolment.Kind}
	if enrolment.Kind != enrolBootstrap {
		info["email"], info["name"] = enrolment.Account.Email, enrolment.Account.Name
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *Handler) enrolBegin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rp, err := h.relyingParty(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		Token string `json:"token"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.Token == "" || len(req.Token) > 128 {
		writeError(w, http.StatusBadRequest, "this enrolment link is not valid")
		return
	}
	hash := sha256.Sum256([]byte(req.Token))
	if id, ok := h.devices.peek(req.Token); ok {
		account, credentials, found := h.accountWithCredentials(r.Context(), id)
		if !found {
			writeError(w, http.StatusUnauthorized, "this link expired or was already used; ask for a new one")
			return
		}
		creation, session, err := rp.BeginRegistration(&webUser{account: account, credentials: credentials},
			webauthn.WithExclusions(webauthn.Credentials(credentials).CredentialDescriptors()))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "adding a passkey could not start")
			return
		}
		ceremony, err := h.ceremonies.put(pendingCeremony{origin: r.Header.Get("Origin"), enrol: true, kind: "device", session: *session, tokenHash: hash[:], account: account})
		if err != nil {
			writeError(w, http.StatusTooManyRequests, "too many sign-ins in progress; try again shortly")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ceremony": ceremony, "options": creation})
		return
	}
	enrolment, err := h.identities.Enrolment(r.Context(), hash[:])
	if err != nil {
		h.failedSignIn(r, "enrolment_invalid", err)
		writeError(w, http.StatusUnauthorized, "this enrolment link expired or was already used; ask the host for a new one")
		return
	}
	account := enrolment.Account
	if enrolment.Kind == enrolBootstrap || enrolment.Kind == enrolInvite {
		name, ok := cleanName(req.Name)
		email := strings.ToLower(strings.TrimSpace(req.Email))
		tools := operatorTools
		if enrolment.Kind == enrolInvite {
			// The invitation fixed who this is and what they may change.
			email, tools = enrolment.Account.Email, enrolment.Account.Tools
			if name == "" {
				name, ok = cleanName(enrolment.Account.Name)
			}
		}
		if !ok || !validEmail(email) {
			writeError(w, http.StatusBadRequest, "give your full name and a valid email")
			return
		}
		handle := make([]byte, 32)
		id := make([]byte, 16)
		if _, err := rand.Read(handle); err != nil {
			writeError(w, http.StatusServiceUnavailable, "enrolment could not start")
			return
		}
		if _, err := rand.Read(id); err != nil {
			writeError(w, http.StatusServiceUnavailable, "enrolment could not start")
			return
		}
		account = adminid.Account{ID: hex.EncodeToString(id), Handle: handle, Name: name, Email: email, Tools: tools}
	}
	creation, session, err := rp.BeginRegistration(&webUser{account: account})
	if err != nil {
		h.failedSignIn(r, "enrol_begin_failed", err)
		writeError(w, http.StatusInternalServerError, "enrolment could not start")
		return
	}
	id, err := h.ceremonies.put(pendingCeremony{origin: r.Header.Get("Origin"), enrol: true, kind: enrolment.Kind, session: *session, tokenHash: hash[:], account: account})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "too many sign-ins in progress; try again shortly")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ceremony": id, "options": creation})
}

func (h *Handler) enrolFinish(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rp, err := h.relyingParty(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pending, ok := h.ceremonies.take(r.Header.Get(adminCeremony), true, r.Header.Get("Origin"))
	if !ok {
		h.failedSignIn(r, "ceremony_unknown", nil)
		writeError(w, http.StatusUnauthorized, "this step expired; start again")
		return
	}
	if pending.kind == "device" {
		h.finishDevice(w, r, rp, pending)
		return
	}
	if pending.kind == "add" {
		writeError(w, http.StatusUnauthorized, "this step expired; start again")
		return
	}
	credential, err := rp.FinishRegistration(&webUser{account: pending.account}, pending.session, r)
	if err != nil {
		h.failedSignIn(r, "enrol_invalid", err)
		writeError(w, http.StatusUnauthorized, "the passkey could not be verified; try again")
		return
	}
	h.identityMu.Lock()
	defer h.identityMu.Unlock()
	account, err := h.identities.CompleteEnrolment(r.Context(), pending.tokenHash, pending.account, *credential, deviceLabel(r.UserAgent()))
	if err != nil {
		h.failedSignIn(r, "enrol_store_failed", err)
		if errors.Is(err, adminid.ErrEnrolmentInvalid) || errors.Is(err, adminid.ErrAlreadyEnrolled) {
			writeError(w, http.StatusUnauthorized, "this enrolment link expired or was already used; ask the host for a new one")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "the passkey could not be saved")
		return
	}
	if pending.kind == enrolRecover {
		h.operators.revokeAccount(account.ID)
		h.devices.revokeAccount(account.ID)
	}
	h.emit(events.Event{Type: events.Type("admin.passkey_registered"), Actor: "admin:" + account.Email, Code: account.ID})
	h.startAdminSession(w, account, credential.ID)
}

func (h *Handler) adminLoginBegin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rp, err := h.relyingParty(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	assertion, session, err := rp.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		h.failedSignIn(r, "begin_failed", err)
		writeError(w, http.StatusInternalServerError, "sign-in could not start")
		return
	}
	id, err := h.ceremonies.put(pendingCeremony{origin: r.Header.Get("Origin"), session: *session})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "too many sign-ins in progress; try again shortly")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ceremony": id, "options": assertion})
}

func (h *Handler) adminLoginFinish(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rp, err := h.relyingParty(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pending, ok := h.ceremonies.take(r.Header.Get(adminCeremony), false, r.Header.Get("Origin"))
	if !ok {
		h.failedSignIn(r, "ceremony_unknown", nil)
		writeError(w, http.StatusUnauthorized, "this step expired; start again")
		return
	}
	var found adminid.Account
	lookup := func(_, handle []byte) (webauthn.User, error) {
		account, credentials, err := h.identities.AccountByHandle(r.Context(), handle)
		if err != nil {
			return nil, err
		}
		found = account
		return &webUser{account: account, credentials: credentials}, nil
	}
	_, credential, err := rp.FinishPasskeyLogin(lookup, pending.session, r)
	if err != nil || found.ID == "" {
		h.failedSignIn(r, "passkey_invalid", err)
		writeError(w, http.StatusUnauthorized, "that passkey is not an administrator of this edge")
		return
	}
	if credential.Authenticator.CloneWarning {
		h.failedSignIn(r, "passkey_clone_warning", nil)
		writeError(w, http.StatusUnauthorized, "that passkey was refused; ask the host to recover your access")
		return
	}
	h.identityMu.Lock()
	defer h.identityMu.Unlock()
	found, _, err = h.identities.AccountByHandle(r.Context(), found.Handle)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "that administrator is no longer active")
		return
	}
	if err := h.identities.RecordLogin(r.Context(), *credential); err != nil {
		h.failedSignIn(r, "passkey_record_failed", err)
		writeError(w, http.StatusUnauthorized, "that passkey is no longer active")
		return
	}
	h.startAdminSession(w, found, credential.ID)
}

// startAdminSession turns a verified administrator into the same short,
// memory-only operator session the host login produces.
func (h *Handler) startAdminSession(w http.ResponseWriter, account adminid.Account, credentialID []byte) {
	token, grant, err := h.operators.issueSliding(account.Name, account.Tools...)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the session could not be opened")
		return
	}
	h.operators.bind(grant.ID, account.ID, credentialID)
	h.emit(events.Event{Type: events.Type("operator.signed_in"), Actor: "admin:" + account.Email, Code: grant.ID, Reason: "passkey"})
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "name": grant.Name, "id": grant.ID, "expires": grant.Expires, "tools": grant.Tools, "renewable": true})
}

type adminView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Email      string   `json:"email"`
	Tools      []string `json:"tools"`
	Disabled   bool     `json:"disabled"`
	Passkeys   int      `json:"passkeys"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt string   `json:"last_used_at,omitempty"`
	You        bool     `json:"you,omitempty"`
}

func (h *Handler) listAdmins(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	accounts, err := h.identities.Accounts(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "administrator directory unavailable")
		return
	}
	self := h.callerAccount(r)
	out := make([]adminView, 0, len(accounts))
	for _, a := range accounts {
		view := adminView{ID: a.ID, Name: a.Name, Email: a.Email, Tools: a.Tools, Disabled: !a.DisabledAt.IsZero(), Passkeys: a.Passkeys, CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339), You: a.ID == self}
		if !a.LastUsedAt.IsZero() {
			view.LastUsedAt = a.LastUsedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": out, "tools": operatorTools})
}

// callerAccount is the administrator behind this request's operator session,
// or "" for the admin token, the host socket or a host login.
func (h *Handler) callerAccount(r *http.Request) string {
	grant, ok := r.Context().Value(operatorKey{}).(operatorGrant)
	if !ok {
		return ""
	}
	return h.operators.account(grant.ID)
}

// inviteAdmin lets someone with the administrators tool add another person
// with a chosen subset of tools. A grant can never exceed its giver's own
// tools, so delegating cannot widen authority.
func (h *Handler) inviteAdmin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string   `json:"email"`
		Name  string   `json:"name"`
		Tools []string `json:"tools"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "email and tools required")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(email) {
		writeError(w, http.StatusBadRequest, "give a valid email")
		return
	}
	name := ""
	if strings.TrimSpace(req.Name) != "" {
		var ok bool
		if name, ok = cleanName(req.Name); !ok {
			writeError(w, http.StatusBadRequest, "name must be 1 to 80 characters without control characters")
			return
		}
	}
	tools, err := normalizeTools(req.Tools)
	if err != nil || len(req.Tools) == 0 {
		writeError(w, http.StatusBadRequest, "choose at least one tool; known tools: "+strings.Join(operatorTools, ", "))
		return
	}
	if grant, ok := r.Context().Value(operatorKey{}).(operatorGrant); ok {
		for _, tool := range tools {
			if !grant.allows(tool) {
				writeError(w, http.StatusForbidden, "you cannot grant the "+tool+" tool: this session does not include it")
				return
			}
		}
	}
	if !h.recordMutationIntent(w, r, "admin.invite", email) {
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusServiceUnavailable, "invitation could not be issued")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	expires := time.Now().Add(24 * time.Hour)
	switch err := h.identities.IssueEnrolment(r.Context(), adminid.EnrolmentRequest{Kind: enrolInvite, Email: email, Name: name, Tools: tools}, hash[:], expires); {
	case errors.Is(err, adminid.ErrEmailTaken):
		writeError(w, http.StatusConflict, "that email already is an administrator; use recovery on the host if they lost access")
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "invitation could not be issued")
		return
	}
	h.emit(events.Event{Type: events.Type("admin.invited"), Actor: h.actorOf(r), Host: email, Reason: "tools: " + strings.Join(tools, ",")})
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expires": expires, "email": email, "tools": tools})
}

// recoverTTL is how long a recovery link an administrator hands over lasts.
const recoverTTL = time.Hour

// recoverAdmin lets an administrator give another one a way back in after
// losing every passkey: a one-use link that replaces their passkeys. It is the
// console's counterpart of `console recover`, so nobody has to reach the host
// for it. Whoever holds the link becomes that account, so the caller needs
// every tool the target has: recovery cannot be used to climb.
func (h *Handler) recoverAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == h.callerAccount(r) {
		writeError(w, http.StatusConflict, "you are signed in; add another passkey instead")
		return
	}
	accounts, err := h.identities.Accounts(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "administrator directory unavailable")
		return
	}
	var target *adminid.Summary
	for i := range accounts {
		if accounts[i].ID == id && accounts[i].DisabledAt.IsZero() {
			target = &accounts[i]
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "no such active administrator")
		return
	}
	if grant, ok := r.Context().Value(operatorKey{}).(operatorGrant); ok {
		for _, tool := range target.Tools {
			if !grant.allows(tool) {
				writeError(w, http.StatusForbidden, "you cannot recover this administrator: they hold the "+tool+" tool and this session does not")
				return
			}
		}
	}
	if !h.recordMutationIntent(w, r, "admin.recover", target.Email) {
		return
	}
	token, hash, err := newEnrolmentToken()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the link could not be issued")
		return
	}
	expires := time.Now().Add(recoverTTL)
	if err := h.identities.IssueEnrolment(r.Context(), adminid.EnrolmentRequest{Kind: enrolRecover, Email: target.Email}, hash, expires); err != nil {
		writeError(w, http.StatusServiceUnavailable, "the link could not be issued")
		return
	}
	h.emit(events.Event{Type: events.Type("admin.recovery_issued"), Actor: h.actorOf(r), Host: target.Email})
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expires": expires, "email": target.Email})
}

// addPasskeyBegin starts adding another passkey to the signed-in
// administrator's own account, so losing one device never locks them out.
func (h *Handler) addPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	self := h.callerAccount(r)
	if self == "" {
		writeError(w, http.StatusForbidden, "sign in with your passkey to add another one")
		return
	}
	rp, err := h.relyingParty(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	account, credentials, ok := h.accountWithCredentials(r.Context(), self)
	if !ok {
		writeError(w, http.StatusUnauthorized, "that administrator is no longer active")
		return
	}
	creation, session, err := rp.BeginRegistration(&webUser{account: account, credentials: credentials},
		webauthn.WithExclusions(webauthn.Credentials(credentials).CredentialDescriptors()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "adding a passkey could not start")
		return
	}
	id, err := h.ceremonies.put(pendingCeremony{origin: r.Header.Get("Origin"), enrol: true, kind: "add", session: *session, account: account})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "too many sign-ins in progress; try again shortly")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ceremony": id, "options": creation})
}

func (h *Handler) addPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	self := h.callerAccount(r)
	pending, ok := h.ceremonies.take(r.Header.Get(adminCeremony), true, r.Header.Get("Origin"))
	if self == "" || !ok || pending.kind != "add" || pending.account.ID != self {
		h.failedSignIn(r, "add_passkey_unknown", nil)
		writeError(w, http.StatusUnauthorized, "this step expired; start again")
		return
	}
	rp, err := h.relyingParty(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	credential, err := rp.FinishRegistration(&webUser{account: pending.account}, pending.session, r)
	if err != nil {
		h.failedSignIn(r, "add_passkey_invalid", err)
		writeError(w, http.StatusUnauthorized, "the passkey could not be verified; try again")
		return
	}
	h.identityMu.Lock()
	defer h.identityMu.Unlock()
	// The session was checked before this lock. A recovery or a disable that
	// ran meanwhile revoked it under the same lock, so check again now: a
	// passkey must never be attached after the account's sessions ended.
	if h.callerAccount(r) != self {
		h.failedSignIn(r, "add_passkey_session_revoked", nil)
		writeError(w, http.StatusUnauthorized, "your session ended; sign in again")
		return
	}
	if err := h.identities.AddPasskey(r.Context(), self, *credential, deviceLabel(r.UserAgent())); err != nil {
		writeError(w, http.StatusServiceUnavailable, "the passkey could not be saved")
		return
	}
	h.emit(events.Event{Type: events.Type("admin.passkey_added"), Actor: h.actorOf(r), Host: pending.account.Email})
	writeJSON(w, http.StatusOK, map[string]bool{"added": true})
}

// issueDeviceLink gives the signed-in administrator a one-use link to open in
// another browser, where it creates a passkey for the same account. It exists
// because passkeys are kept per password manager: one saved by Chrome is not
// offered by Firefox.
func (h *Handler) issueDeviceLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	self := h.callerAccount(r)
	if self == "" {
		writeError(w, http.StatusForbidden, "sign in with your passkey to add another browser")
		return
	}
	token, expires, err := h.devices.issue(self)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the link could not be issued")
		return
	}
	h.emit(events.Event{Type: events.Type("admin.device_link_issued"), Actor: h.actorOf(r)})
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expires": expires})
}

// finishDevice completes a passkey created through a device link and signs
// that browser in. The link is used up under the identity lock, so a recovery
// or a disable that ended it meanwhile wins.
func (h *Handler) finishDevice(w http.ResponseWriter, r *http.Request, rp *webauthn.WebAuthn, pending pendingCeremony) {
	credential, err := rp.FinishRegistration(&webUser{account: pending.account}, pending.session, r)
	if err != nil {
		h.failedSignIn(r, "device_passkey_invalid", err)
		writeError(w, http.StatusUnauthorized, "the passkey could not be verified; try again")
		return
	}
	h.identityMu.Lock()
	defer h.identityMu.Unlock()
	if !h.devices.take(pending.tokenHash, pending.account.ID) {
		writeError(w, http.StatusUnauthorized, "this link expired or was already used; ask for a new one")
		return
	}
	account, _, found := h.accountWithCredentials(r.Context(), pending.account.ID)
	if !found {
		writeError(w, http.StatusUnauthorized, "that administrator is no longer active")
		return
	}
	if err := h.identities.AddPasskey(r.Context(), account.ID, *credential, deviceLabel(r.UserAgent())); err != nil {
		writeError(w, http.StatusServiceUnavailable, "the passkey could not be saved")
		return
	}
	h.emit(events.Event{Type: events.Type("admin.passkey_added"), Actor: "admin:" + account.Email, Reason: "another browser"})
	h.startAdminSession(w, account, credential.ID)
}

type passkeyView struct {
	ID         string `json:"id"`
	Label      string `json:"label,omitempty"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	Current    bool   `json:"current,omitempty"`
}

// listPasskeys shows the signed-in administrator their own active passkeys
// and which one opened this session.
func (h *Handler) listPasskeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	self, current := h.callerPasskey(r)
	if self == "" {
		writeError(w, http.StatusForbidden, "sign in with your passkey to see your passkeys")
		return
	}
	passkeys, err := h.identities.Passkeys(r.Context(), self)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "passkeys unavailable")
		return
	}
	out := make([]passkeyView, 0, len(passkeys))
	for _, p := range passkeys {
		view := passkeyView{ID: base64.RawURLEncoding.EncodeToString(p.ID), Label: p.Label, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339), Current: string(p.ID) == current}
		if !p.LastUsedAt.IsZero() {
			view.LastUsedAt = p.LastUsedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": out})
}

// removePasskey lets an administrator revoke one of their own passkeys, for
// a lost phone or a browser they no longer use. The passkey that opened this
// session cannot be removed from it, which also keeps the last one; the
// sessions the removed passkey opened end at once.
func (h *Handler) removePasskey(w http.ResponseWriter, r *http.Request) {
	self, current := h.callerPasskey(r)
	if self == "" {
		writeError(w, http.StatusForbidden, "sign in with your passkey to remove one")
		return
	}
	id, err := base64.RawURLEncoding.DecodeString(r.PathValue("id"))
	if err != nil || len(id) == 0 {
		writeError(w, http.StatusNotFound, "no such passkey")
		return
	}
	if string(id) == current {
		writeError(w, http.StatusConflict, "this session was opened with that passkey; sign in with another one to remove it")
		return
	}
	if !h.recordMutationIntent(w, r, "admin.passkey_remove", "") {
		return
	}
	h.identityMu.Lock()
	defer h.identityMu.Unlock()
	if owner, _ := h.callerPasskey(r); owner != self {
		writeError(w, http.StatusUnauthorized, "your session ended; sign in again")
		return
	}
	switch err := h.identities.RemovePasskey(r.Context(), self, id); {
	case errors.Is(err, adminid.ErrPasskeyNotFound):
		writeError(w, http.StatusNotFound, "no such passkey")
		return
	case errors.Is(err, adminid.ErrLastPasskey):
		writeError(w, http.StatusConflict, "this is your only passkey; add another before removing it")
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "the passkey could not be removed")
		return
	}
	h.operators.revokeCredential(id)
	h.emit(events.Event{Type: events.Type("admin.passkey_removed"), Actor: h.actorOf(r)})
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// callerPasskey is the administrator behind this request's session and the
// passkey that opened it; both are "" for the admin token or a host login.
func (h *Handler) callerPasskey(r *http.Request) (account, credential string) {
	grant, ok := r.Context().Value(operatorKey{}).(operatorGrant)
	if !ok {
		return "", ""
	}
	return h.operators.account(grant.ID), h.operators.credential(grant.ID)
}

// deviceLabel names where a passkey was made, from the browser's User-Agent:
// "Firefox on Linux", "Chrome on Android". It is a hint for the person who
// owns the passkey, never an authorization input.
func deviceLabel(userAgent string) string {
	browser := ""
	for _, b := range []struct{ token, name string }{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"FxiOS/", "Firefox"},
		{"CriOS/", "Chrome"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"},
	} {
		if strings.Contains(userAgent, b.token) {
			browser = b.name
			break
		}
	}
	system := ""
	for _, o := range []struct{ token, name string }{
		{"Android", "Android"}, {"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Windows", "Windows"},
		{"Mac OS X", "macOS"}, {"CrOS", "ChromeOS"}, {"Linux", "Linux"},
	} {
		if strings.Contains(userAgent, o.token) {
			system = o.name
			break
		}
	}
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	default:
		return system
	}
}

// accountWithCredentials finds an enabled administrator by ID with their
// active passkeys.
func (h *Handler) accountWithCredentials(ctx context.Context, id string) (adminid.Account, []webauthn.Credential, bool) {
	accounts, err := h.identities.Accounts(ctx)
	if err != nil {
		return adminid.Account{}, nil, false
	}
	for _, a := range accounts {
		if a.ID == id {
			account, credentials, err := h.identities.AccountByHandle(ctx, a.Handle)
			return account, credentials, err == nil
		}
	}
	return adminid.Account{}, nil, false
}

func (h *Handler) actorOf(r *http.Request) string {
	if grant, ok := r.Context().Value(operatorKey{}).(operatorGrant); ok {
		return "operator:" + grant.Name
	}
	return "local"
}

func (h *Handler) setAdminDisabled(disabled bool) http.HandlerFunc {
	action := "admin.enable"
	if disabled {
		action = "admin.disable"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if disabled && id == h.callerAccount(r) {
			writeError(w, http.StatusConflict, "you cannot disable your own account; ask another administrator or the host")
			return
		}
		if !h.recordMutationIntent(w, r, action, id) {
			return
		}
		h.identityMu.Lock()
		defer h.identityMu.Unlock()
		account, err := h.identities.SetDisabled(r.Context(), id, disabled)
		switch {
		case errors.Is(err, adminid.ErrAccountNotFound):
			writeError(w, http.StatusNotFound, "no such administrator")
			return
		case errors.Is(err, adminid.ErrLastAdministrator):
			writeError(w, http.StatusConflict, "this is the last enabled administrator who can manage administrators")
			return
		case err != nil:
			writeError(w, http.StatusServiceUnavailable, "the change could not be saved")
			return
		}
		if disabled {
			// Their open sessions and links end now, not when they expire.
			h.operators.revokeAccount(id)
			h.devices.revokeAccount(id)
		}
		h.emit(events.Event{Type: events.Type(action + "d"), Actor: h.actorOf(r), Host: account.Email})
		writeJSON(w, http.StatusOK, map[string]bool{"disabled": disabled})
	}
}
