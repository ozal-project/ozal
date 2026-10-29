package passkey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
)

// Host sessions behind the central login (ID-015). A protected host the
// session cookie cannot reach sends the browser to the login host with a
// transaction; the login host answers with a one-time code; the host
// redeems it for its own host-only session. The protocol and its threats
// are in docs/architecture/host-sessions.md.
const (
	continuePath = "/_/auth/continue"
	handoffPath  = "/_/auth/handoff"
	// transactionTTL covers a login or an enrolment done on the way.
	transactionTTL = 10 * time.Minute
	// codeTTL is how long a code may travel from the login host to the app.
	codeTTL     = time.Minute
	maxHandoffs = 10_000
)

var (
	errHandoffsFull   = errors.New("passkey: too many pending sign-ins")
	errHandoffUnknown = errors.New("passkey: sign-in transaction unknown or expired")
)

// transaction is one app host's request to the login host.
type transaction struct {
	host     string
	returnTo string // a path on host
	binding  [32]byte
	// bindingID gives each pending transaction its own cookie. It is the
	// public transaction ID, never the binding secret or the approval code.
	bindingID string
	// reason is the login page's step-up query ("reauth=1", ...); with one
	// set, only a central login newer than created approves the transaction.
	reason  string
	created time.Time
	expires time.Time
}

// code is an approved transaction waiting to be redeemed on its host.
type code struct {
	transaction
	parent  string // the central session that approved it
	expires time.Time
}

// handoffs keeps transactions and codes in memory. A restart forgets them,
// which costs a visitor one retry; it never weakens a check.
type handoffs struct {
	now func() time.Time

	mu           sync.Mutex
	transactions map[string]transaction
	codes        map[string]code
}

func newHandoffs(now func() time.Time) *handoffs {
	return &handoffs{now: now, transactions: make(map[string]transaction), codes: make(map[string]code)}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// room makes space for one more entry, dropping expired ones first. The
// caller holds the lock.
func (h *handoffs) room(now time.Time) bool {
	if len(h.transactions)+len(h.codes) < maxHandoffs {
		return true
	}
	for id, t := range h.transactions {
		if !now.Before(t.expires) {
			delete(h.transactions, id)
		}
	}
	for id, c := range h.codes {
		if !now.Before(c.expires) {
			delete(h.codes, id)
		}
	}
	return len(h.transactions)+len(h.codes) < maxHandoffs
}

func (h *handoffs) begin(t transaction) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	now := h.now()
	t.created = now.Truncate(time.Second) // auth times are stored in seconds
	t.expires = now.Add(transactionTTL)
	t.bindingID = id
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.room(now) {
		return "", errHandoffsFull
	}
	h.transactions[id] = t
	return id, nil
}

// pending returns a live transaction without consuming it: the browser may
// still have to sign in before it is approved.
func (h *handoffs) pending(id string) (transaction, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.transactions[id]
	if !ok || !h.now().Before(t.expires) {
		delete(h.transactions, id)
		return transaction{}, false
	}
	return t, true
}

// approve consumes the transaction and issues its one-time code.
func (h *handoffs) approve(id, parent string) (string, transaction, error) {
	value, err := randomToken()
	if err != nil {
		return "", transaction{}, err
	}
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.transactions[id]
	delete(h.transactions, id)
	if !ok || !now.Before(t.expires) {
		return "", transaction{}, errHandoffUnknown
	}
	if !h.room(now) {
		return "", transaction{}, errHandoffsFull
	}
	h.codes[value] = code{transaction: t, parent: parent, expires: now.Add(codeTTL)}
	return value, t, nil
}

// redeem consumes a code. Every attempt consumes it, successful or not, so
// a stolen code cannot be retried with guessed bindings.
func (h *handoffs) redeem(value string) (code, bool) {
	h.mu.Lock()
	c, ok := h.codes[value]
	delete(h.codes, value)
	h.mu.Unlock()
	if !ok || !h.now().Before(c.expires) {
		return code{}, false
	}
	return c, true
}

// handoffHost reports whether host gets host sessions from the login host:
// a protected host other than the login host that cookie_domain does not
// cover.
func (g *Gate) handoffHost(r *http.Request) (string, bool) {
	host, err := config.NormalizeHost(r.Host)
	if err != nil {
		return "", false
	}
	return host, g.handoffHosts[host]
}

// audience is the session audience valid on r's host: the host itself for
// a handoff host, "" (central) everywhere else.
func (g *Gate) audience(r *http.Request) string {
	host, ok := g.handoffHost(r)
	if !ok {
		return ""
	}
	return host
}

func (g *Gate) scheme(r *http.Request) string {
	if r.TLS != nil || g.secure {
		return "https"
	}
	return "http"
}

// beginHandoff starts a transaction for a visitor of a handoff host and
// sends the browser to the login host's continue endpoint.
func (g *Gate) beginHandoff(w http.ResponseWriter, r *http.Request, host, reason string) {
	binding, err := randomToken()
	if err != nil {
		http.Error(w, "Something went wrong on the server.", http.StatusInternalServerError)
		return
	}
	returnTo := r.URL.RequestURI()
	if !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") || strings.HasPrefix(returnTo, "/\\") {
		returnTo = "/"
	}
	id, err := g.handoffs.begin(transaction{host: host, returnTo: returnTo, binding: sha256.Sum256([]byte(binding)), reason: reason})
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Code: "handoff_busy"})
		http.Error(w, errBusy.message, http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.HandoffCookiePrefix + id,
		Value:    binding,
		Path:     handoffPath,
		MaxAge:   int(transactionTTL.Seconds()),
		HttpOnly: true,
		Secure:   g.secure,
		// Lax: the browser comes back from the login host by a top-level
		// GET, the one cross-site request Lax lets the cookie ride on.
		SameSite: http.SameSiteLaxMode,
	})
	noStore(w)
	http.Redirect(w, r, g.origin+continuePath+"?tx="+url.QueryEscape(id), http.StatusFound)
}

// serveContinue runs on the login host: it approves a transaction for the
// signed-in visitor, or sends them to sign in first and come back.
func (g *Gate) serveContinue(w http.ResponseWriter, r *http.Request) {
	// GET only: approving and redeeming consume state, which a HEAD from a
	// link checker should not do.
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	noStore(w)
	id := r.URL.Query().Get("tx")
	t, ok := g.handoffs.pending(id)
	if !ok {
		g.emit(r, events.Event{Type: events.LoginFailed, Code: "handoff_transaction_unknown"})
		http.Error(w, "This sign-in expired. Go back to the application and open it again.", http.StatusBadRequest)
		return
	}
	principal, signedIn := g.Authenticate(r)
	if !signedIn || (t.reason != "" && principal.AuthTime.Before(t.created)) {
		target := g.origin + "/_/auth/login?return_to=" + url.QueryEscape(g.origin+continuePath+"?tx="+url.QueryEscape(id))
		if t.reason != "" {
			target += "&" + t.reason
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	central, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		http.Error(w, errSignedOut.message, http.StatusUnauthorized)
		return
	}
	value, t, err := g.handoffs.approve(id, central.Value)
	if err != nil {
		g.emit(r, events.Event{Type: events.LoginFailed, Actor: principal.Subject, Code: "handoff_approve_failed"})
		http.Error(w, "This sign-in expired. Go back to the application and open it again.", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, g.scheme(r)+"://"+t.host+handoffPath+"?code="+url.QueryEscape(value), http.StatusFound)
}

// serveHandoff runs on a handoff host: it redeems a code from the browser
// that started its transaction for a host session, and goes back to where
// the visitor was going.
func (g *Gate) serveHandoff(w http.ResponseWriter, r *http.Request, host string) {
	// GET only: approving and redeeming consume state, which a HEAD from a
	// link checker should not do.
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	noStore(w)
	refuse := func(reason string) {
		g.emit(r, events.Event{Type: events.LoginFailed, Code: reason})
		http.Error(w, "This sign-in link is invalid or expired. Open the application again.", http.StatusBadRequest)
	}
	c, ok := g.handoffs.redeem(r.URL.Query().Get("code"))
	if !ok {
		refuse("handoff_code_invalid")
		return
	}
	if c.host != host {
		refuse("handoff_wrong_host")
		return
	}
	// Only clear this transaction's cookie. Another tab may still need its
	// own binding, whether this redemption succeeds or fails.
	name := auth.HandoffCookiePrefix + c.bindingID
	http.SetCookie(w, &http.Cookie{Name: name, Path: handoffPath, MaxAge: -1, HttpOnly: true, Secure: g.secure, SameSite: http.SameSiteLaxMode})
	binding, err := r.Cookie(name)
	if err != nil {
		refuse("handoff_binding_missing")
		return
	}
	if sum := sha256.Sum256([]byte(binding.Value)); subtle.ConstantTimeCompare(sum[:], c.binding[:]) != 1 {
		refuse("handoff_binding_mismatch")
		return
	}
	id, principal, err := g.linked.CreateLinked(r.Context(), c.parent, host)
	switch {
	case errors.Is(err, auth.ErrStoreFull):
		g.emit(r, events.Event{Type: events.LoginFailed, Code: "session_store_full"})
		http.Error(w, errBusy.message, http.StatusServiceUnavailable)
		return
	case errors.Is(err, auth.ErrSessionNotFound):
		refuse("handoff_login_ended")
		return
	case err != nil:
		g.emit(r, events.Event{Type: events.LoginFailed, Code: "session_create_failed", Error: err.Error()})
		http.Error(w, errInternal.message, http.StatusInternalServerError)
		return
	}
	// Never keep a session the browser had before this sign-in.
	if previous, err := r.Cookie(auth.SessionCookieName); err == nil {
		g.sessions.Delete(r.Context(), previous.Value)
	}
	http.SetCookie(w, auth.SessionCookie(id, "", g.secure, g.lifetime))
	g.emit(r, events.Event{Type: events.LoginSucceeded, Actor: principal.Subject, Code: "handoff"})
	http.Redirect(w, r, c.returnTo, http.StatusSeeOther)
}

// noStore keeps transaction IDs and codes out of caches and Referer headers.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}
