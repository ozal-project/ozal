package admin

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ozal-project/ozal/internal/adminid"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/enrolcode"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/ipcountry"
	"github.com/ozal-project/ozal/internal/passkey"
	"github.com/ozal-project/ozal/internal/pipeline"
	"github.com/ozal-project/ozal/internal/useragent"
)

// Device-code enrolment: `ozal console enrol` prints a short code and the
// person types it on the login host from any device. The rest happens on that
// page: it shows which server asked, who ran the command and from where, the
// person says who they are and creates the passkey, or withdraws the code if
// the request is not theirs. The terminal only shows the code and reports the
// outcome. The decisions live in internal/enrolcode; this file stores the
// enrolment token, serves the page and runs the ceremony.
//
// The login host answers these paths only while a code from the host is
// live; otherwise they fall through as unknown paths, like /_/auth/admin
// without a transaction.

const (
	codePath     = remotePath + "/code"
	claimHeader  = "X-Ozal-Claim"
	codeTxPrefix = "code:"
)

func (h *Handler) registerCodeEnrolment() {
	h.mux.HandleFunc("GET /_/api/admin/enrolments/code/{poll}", h.requireLocal(h.watchCode))
	h.mux.HandleFunc("DELETE /_/api/admin/enrolments/code/{poll}", h.requireLocal(h.cancelCode))
}

// countries names the country of an address from a local table, for people to
// read; it never decides anything. *ipcountry.Table implements it.
type countries interface {
	Country(addr netip.Addr) string
}

// CountryCredit is the attribution shown under the countries, as the table's
// licence asks.
type CountryCredit struct {
	Text string
	URL  string
}

// WithCountries lets the code page show the country of the addresses it
// lists (admin.country_database), credited as credit says.
func WithCountries(c countries, credit CountryCredit) Option {
	return func(h *Handler) { h.countries, h.countryCredit = c, credit }
}

// countryOf is "" without a table or for an address it does not cover.
func (h *Handler) countryOf(s string) string {
	addr, err := netip.ParseAddr(s)
	if h.countries == nil || err != nil {
		return ""
	}
	return h.countries.Country(addr)
}

// codeRequest is who asked for a code on the host: the terminal sends the
// name of the person who ran the command and the address of their SSH
// session, if any.
type codeRequest struct {
	Operator string
	From     string
}

// issueCode prints a code instead of a link. It needs admin.login_host: the
// code is typed on the public login host, where the passkey is created. The
// code is reserved in the book before the enrolment is stored, so a full book
// changes nothing (a recovery's earlier link keeps working).
func (h *Handler) issueCode(w http.ResponseWriter, r *http.Request, kind, email string, who codeRequest) {
	_, loginOrigin, ok := h.loginSite()
	if !ok {
		writeErrorCode(w, http.StatusPreconditionFailed, "enrolment codes need admin.login_host and passkeys on the login host; use the link instead", "login_host_off")
		return
	}
	operator, ok := cleanName(who.Operator)
	if !ok {
		operator = strings.TrimPrefix(localActor(r), "local:")
	}
	from := ""
	if addr, err := netip.ParseAddr(strings.TrimSpace(who.From)); err == nil {
		from = addr.Unmap().String()
	}
	server, _ := os.Hostname()
	if name, ok := cleanName(server); ok {
		server = name
	} else {
		server = ""
	}
	token, hash, err := newEnrolmentToken()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "enrolment could not be issued")
		return
	}
	// The code is reserved first: a full book answers before anything is
	// recorded or stored.
	issued, err := h.codes.Issue(enrolcode.Request{Kind: kind, Email: email, Server: server, Operator: operator, From: from, Token: token})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	if !h.recordMutationIntent(w, r, "admin.enrolment", kind+"/code") || !h.storeEnrolment(w, r, kind, email, hash, time.Now().Add(enrolcode.Lifetime)) {
		h.codes.Cancel(issued.Poll)
		return
	}
	if kind == enrolRecover {
		// Storing a recovery retired earlier unused recoveries of the same
		// administrator; end their codes so those terminals stop waiting.
		h.codes.Supersede(issued.Poll, kind, email)
	}
	h.emit(events.Event{Type: events.Type("admin.enrolment_code_issued"), Actor: localActor(r), Code: kind})
	writeJSON(w, http.StatusCreated, map[string]any{
		"poll": issued.Poll, "code": issued.Code, "url": loginOrigin + codePath,
		"claim_by": issued.ClaimBy, "expires": issued.Expires,
	})
}

func codeView(v enrolcode.View) map[string]any {
	out := map[string]any{"state": v.State, "kind": v.Kind, "claim_by": v.ClaimBy, "expires": v.Expires, "failures": v.Failures, "paused": v.Paused}
	if v.Email != "" {
		out["email"] = v.Email
	}
	if v.Account != "" {
		out["account"] = v.Account
	}
	if !v.Claimed.IsZero() {
		out["browser"], out["address"], out["claimed"] = v.Browser.Label, v.Browser.Address, v.Claimed
		if v.Browser.Country != "" {
			out["country"] = v.Browser.Country
		}
	}
	return out
}

func (h *Handler) watchCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	view, ok := h.codes.Watch(r.PathValue("poll"))
	if !ok {
		writeError(w, http.StatusNotFound, "this enrolment expired")
		return
	}
	writeJSON(w, http.StatusOK, codeView(view))
}

// cancelCode is the terminal withdrawing its code. Once the passkey is being
// stored it is refused, so the terminal never reports a cancellation that
// did not happen.
func (h *Handler) cancelCode(w http.ResponseWriter, r *http.Request) {
	if d := h.codes.Cancel(r.PathValue("poll")); !d.Allowed {
		writeErrorCode(w, http.StatusConflict, d.Reason, d.Code)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// visitorAddress is the IP the trust stage decided for this request.
func visitorAddress(r *http.Request) string {
	if ip, ok := pipeline.ClientIP(r.Context()); ok {
		return ip.String()
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// consoleAddress is where the person opens the console once enrolled: its
// loopback port under ozal.localhost, reached through their own tunnel.
func (h *Handler) consoleAddress() string {
	_, port, err := net.SplitHostPort(h.manager.Config().Admin.Listen)
	if err != nil || port == "" {
		_, port, _ = net.SplitHostPort(config.DefaultAdminListen)
	}
	return "http://ozal.localhost:" + port
}

// serveCode answers the login host's code paths. It reports false while no
// code is live, so the path does not exist for the public.
func (rl *RemoteLogin) serveCode(w http.ResponseWriter, r *http.Request, h *Handler, rp *webauthn.WebAuthn, loginOrigin string) bool {
	path := r.URL.Path
	if path != codePath && path != codePath+".js" && path != codePath+".css" && !strings.HasPrefix(path, codePath+"/") {
		return false
	}
	if !h.codes.Live() {
		return false
	}
	post, ok := loginHostRequest(w, r, loginOrigin)
	if !ok {
		return true
	}
	switch {
	case path == codePath && !post:
		// The page never reads a code from its URL: a code must be typed, so
		// nobody can send a link that enrols on someone else's behalf.
		serveAsset(w, "loginhost/admin-code.html", "text/html; charset=utf-8")
	case path == codePath+".js" && !post:
		serveAsset(w, "loginhost/admin-code.js", "text/javascript; charset=utf-8")
	case path == codePath+".css" && !post:
		serveAsset(w, "loginhost/admin-code.css", "text/css; charset=utf-8")
	case strings.HasPrefix(path, codePath+"/flags/") && !post:
		// Flags are SVGs (Radian UI, MIT) named by ISO code; only a two-letter
		// name is looked up, so no other file is reachable. A build without
		// them answers 404 and the page shows the code instead.
		name := strings.TrimSuffix(strings.TrimPrefix(path, codePath+"/flags/"), ".svg")
		if !isFlagName(name) || !strings.HasSuffix(path, ".svg") {
			http.NotFound(w, r)
			return true
		}
		serveFrom(w, h.flags, name+".svg", "image/svg+xml")
	case path == codePath+"/claim" && post:
		h.claimCode(w, r, loginOrigin)
	case path == codePath+"/withdraw" && post:
		if d := h.codes.Withdraw(r.Header.Get(claimHeader)); !d.Allowed {
			writeErrorCode(w, http.StatusConflict, d.Reason, d.Code)
			return true
		}
		h.emit(events.Event{Type: events.Type("admin.enrolment_code_withdrawn"), Actor: "browser"})
		writeJSON(w, http.StatusOK, map[string]string{"state": string(enrolcode.Withdrawn)})
	case path == codePath+"/begin" && post:
		d, req, poll := h.codes.Ceremony(r.Header.Get(claimHeader))
		if !d.Allowed {
			writeErrorCode(w, http.StatusForbidden, d.Reason, d.Code)
			return true
		}
		var who struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := decodeJSON(w, r, &who); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return true
		}
		h.beginEnrol(w, r, rp, enrolRequest{Token: req.Token, Name: who.Name, Email: who.Email}, codeTxPrefix+poll)
	case path == codePath+"/finish" && post:
		h.finishCode(w, r, rp, loginOrigin)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
	return true
}

// finishCode stores the passkey. The enrolment is reserved first, so a
// withdrawal or a host cancellation racing with the store is refused rather
// than reported as done; a failed store releases it for another try.
func (h *Handler) finishCode(w http.ResponseWriter, r *http.Request, rp *webauthn.WebAuthn, loginOrigin string) {
	claim := r.Header.Get(claimHeader)
	d, _, poll := h.codes.Ceremony(claim)
	if !d.Allowed {
		writeErrorCode(w, http.StatusForbidden, d.Reason, d.Code)
		return
	}
	pending, ok := h.ceremonies.take(r.Header.Get(adminCeremony), true, loginOrigin)
	if !ok || pending.tx != codeTxPrefix+poll {
		h.failedSignIn(r, "ceremony_unknown", nil)
		writeError(w, http.StatusUnauthorized, "this step expired; create the passkey again")
		return
	}
	if d, reserved := h.codes.Reserve(claim); !d.Allowed || reserved != poll {
		writeErrorCode(w, http.StatusConflict, d.Reason, d.Code)
		return
	}
	stored := false
	defer func() {
		if !stored {
			h.codes.Release(poll)
		}
	}()
	h.finishEnrol(w, r, rp, pending, func(w http.ResponseWriter, r *http.Request, account adminid.Account, credentialID []byte) {
		stored = true
		h.codes.Finish(poll, account.Name+" <"+account.Email+">")
		if pending.kind == enrolBootstrap {
			// The first administrator exists: other first-run codes can no
			// longer succeed.
			h.codes.Supersede(poll, enrolBootstrap, "")
		}
		h.emit(events.Event{Type: events.Type("admin.enrolment_code_done"), Actor: "admin:" + account.Email})
		answer := map[string]any{
			"state": string(enrolcode.Done), "name": account.Name, "email": account.Email,
			"console": h.consoleAddress(),
		}
		// Where the passkey was saved, for the receipt, only when the
		// provider says so. This page has no console session, so naming the
		// passkey waits for the console.
		if provider, known := h.passkeyProvider(r.Context(), account.ID, credentialID); known {
			answer["provider"] = provider
		}
		writeJSON(w, http.StatusOK, answer)
	})
}

// claimCode is a browser typing the code. The answer tells the person whose
// request this is (server, who ran the command, from where) and where this
// browser connects from, so they can recognise their own or withdraw it. Only
// the holder of a valid code gets it.
func (h *Handler) claimCode(w http.ResponseWriter, r *http.Request, loginOrigin string) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	address := visitorAddress(r)
	browser := enrolcode.Browser{Label: useragent.Label(r.UserAgent()), Address: address, Country: h.countryOf(address)}
	d, claim, view := h.codes.Claim(req.Code, browser)
	if !d.Allowed {
		h.emit(events.Event{Type: events.Type("admin.enrolment_code_refused"), Actor: "browser", Code: d.Code})
		status := http.StatusUnauthorized
		if d.Code == enrolcode.CodeAddressLimit || d.Code == enrolcode.CodePaused {
			status = http.StatusTooManyRequests
		}
		writeErrorCode(w, status, d.Reason, d.Code)
		return
	}
	h.emit(events.Event{Type: events.Type("admin.enrolment_code_claimed"), Actor: "browser", Code: view.Kind})
	out := map[string]any{
		"claim": claim, "kind": view.Kind, "server": view.Server, "operator": view.Operator,
		"issued": view.Issued, "expires": view.Expires,
		// Seconds left by the edge's clock, so the page's countdown does not
		// depend on the device's.
		"expires_in": int(time.Until(view.Expires).Seconds()),
		"host":       strings.TrimPrefix(strings.TrimPrefix(loginOrigin, "https://"), "http://"),
		"browser":    browser.Address,
	}
	if browser.Country != "" {
		out["browser_country"] = browser.Country
	}
	if view.From != "" {
		out["from"] = view.From
		out["from_local"] = isLocalAddress(view.From)
		out["same_network"] = view.From == browser.Address
		if c := h.countryOf(view.From); c != "" {
			out["from_country"] = c
		}
	}
	if (out["browser_country"] != nil || out["from_country"] != nil) && h.countryCredit.Text != "" {
		out["country_credit"] = h.countryCredit.Text
		if h.countryCredit.URL != "" {
			out["country_credit_url"] = h.countryCredit.URL
		}
	}
	if view.Kind != enrolBootstrap {
		out["email"] = view.Email
	}
	writeJSON(w, http.StatusOK, out)
}

// isLocalAddress reports an address on the server's own network, which the
// page labels as such rather than as a place on the Internet.
func isLocalAddress(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && ipcountry.IsLocal(addr)
}

// passkeyProvider names where one of an account's passkeys lives, when its
// provider is known. A lookup that fails only drops the name.
func (h *Handler) passkeyProvider(ctx context.Context, accountID string, credentialID []byte) (string, bool) {
	keys, err := h.identities.Passkeys(ctx, accountID)
	if err != nil {
		return "", false
	}
	for _, k := range keys {
		if bytes.Equal(k.ID, credentialID) {
			name, _, known := passkey.Provider(k.AAGUID, k.Synced)
			return name, known
		}
	}
	return "", false
}

func isFlagName(s string) bool {
	return len(s) == 2 && s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z'
}

// flagAssets is the embedded flags directory. It is not in Git (see
// scripts/flags/import.mjs), so it may be absent; reads then fail with
// fs.ErrNotExist.
func flagAssets() fs.FS {
	sub, _ := fs.Sub(loginHostAssets, "loginhost/flags") // errs only on an invalid path
	return sub
}

func serveAsset(w http.ResponseWriter, name, contentType string) {
	serveFrom(w, loginHostAssets, name, contentType)
}

func serveFrom(w http.ResponseWriter, fsys fs.FS, name, contentType string) {
	body, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(body)
}
