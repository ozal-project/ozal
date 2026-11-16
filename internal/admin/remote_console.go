package admin

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/consoleaccess"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/pipeline"
)

// The remote console (admin.remote) serves the same console on its own HTTPS
// host through the public listener. The admin listener and the socket do not
// change: this is a second door, opt-in, in front of which consoleaccess.Decide
// runs before any handler. What it adds over the loopback console:
//
//   - Only an administrator passkey session opened on this host is accepted.
//     The admin token, the sites token, the host socket and host logins never
//     are, and a session from the loopback console is not either.
//   - Sign-in is the login-host round trip (admin.login_host), started and
//     finished on this origin; enrolment stays on the host or its code flow.
//   - Every change carries the console's exact Origin and the session as a
//     bearer header, which no other site can attach; the resume cookie is
//     __Host- prefixed, Secure, HttpOnly and SameSite=Strict.
//   - Sessions end after remoteIdle without activity and passkeyLimit at
//     most, and all of them end when the host changes or is removed.

// remoteIdle is the idle timeout of a remote console session.
const remoteIdle = 15 * time.Minute

// remoteResumeCookie is the resume cookie on the remote host. The __Host-
// prefix makes the browser refuse it unless it is Secure, host-only and
// Path=/, so a sibling host cannot plant or shadow it.
const remoteResumeCookie = "__Host-ozal_console_session"

// remoteKey marks a request that arrived through the remote console host;
// its value is the console's origin.
type remoteKey struct{}

// remoteOrigin returns the remote console origin when r came through it.
func remoteOrigin(r *http.Request) (string, bool) {
	origin, ok := r.Context().Value(remoteKey{}).(string)
	return origin, ok && origin != ""
}

// RemoteConsole is the remote console's entry from the public edge. It is
// built before the route manager and the admin handler, like RemoteLogin,
// and connected to the handler with WithRemoteConsole.
type RemoteConsole struct {
	handler atomic.Pointer[Handler]
	mu      sync.Mutex
	host    string
}

// NewRemoteConsole returns an entry that answers nothing until it is
// connected to the console's handler.
func NewRemoteConsole() *RemoteConsole { return &RemoteConsole{} }

// WithRemoteConsole lets the edge serve this console on admin.remote.host.
func WithRemoteConsole(remote *RemoteConsole) Option {
	return func(h *Handler) { remote.handler.Store(h) }
}

// Configured is called with every installed configuration. A remote host that
// changes or is removed ends every session opened on it, so turning the
// remote console off and on again does not bring old sessions back.
func (rc *RemoteConsole) Configured(cfg config.Config) {
	host := cfg.RemoteConsoleHost()
	rc.mu.Lock()
	previous := rc.host
	rc.host = host
	rc.mu.Unlock()
	if previous == host || previous == "" {
		return
	}
	if h := rc.handler.Load(); h != nil {
		ended := h.operators.revokeRemote()
		h.emit(events.Event{Type: events.Type("admin.remote_console_closed"), Actor: "config", Host: previous, Reason: "remote console host changed or removed", Count: ended})
	}
}

// ServeHTTP answers a request the edge routed to admin.remote.host.
func (rc *RemoteConsole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := rc.handler.Load()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	host := h.manager.Config().RemoteConsoleHost()
	requested, err := config.NormalizeHost(r.Host)
	if name, _, splitErr := net.SplitHostPort(requested); splitErr == nil {
		requested = name
	}
	if host == "" || err != nil || requested != host {
		http.NotFound(w, r)
		return
	}
	origin := "https://" + host
	decision := consoleaccess.Decide(consoleaccess.Request{
		Method:     r.Method,
		Path:       r.URL.Path,
		HTTPS:      pipeline.PublicHTTPS(r),
		Origin:     r.Header.Get("Origin"),
		WantOrigin: origin,
		Session:    h.remoteSession(r),
	})
	setSecurityHeaders(w, r)
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	if !decision.Allowed {
		if decision.Code != consoleaccess.CodeSignInRequired && decision.Code != consoleaccess.CodeInvalidPath {
			h.emit(events.Event{Type: events.Type("admin.remote_denied"), Actor: "browser", Host: host, Code: decision.Code, Reason: decision.Reason, RemoteAddr: clientAddr(r)})
		}
		switch decision.Code {
		case consoleaccess.CodeCrossOrigin:
			writeErrorCode(w, http.StatusForbidden, decision.Reason, decision.Code)
		case consoleaccess.CodeNotHTTPS:
			http.Error(w, decision.Reason, http.StatusForbidden)
		default:
			// Without a session the console's API does not exist here.
			http.NotFound(w, r)
		}
		return
	}
	ctx := context.WithValue(r.Context(), remoteKey{}, origin)
	// The socket marker can only come from the admin listeners' ConnContext;
	// clear it anyway so nothing on this path can ever act as the host.
	ctx = context.WithValue(ctx, localKey{}, false)
	h.mux.ServeHTTP(w, r.WithContext(ctx))
}

// remoteSession reports whether r carries a live administrator passkey
// session opened on the remote host.
func (h *Handler) remoteSession(r *http.Request) bool {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	grant, ok := h.operators.authenticate(token)
	return ok && grant.Remote && h.operators.account(grant.ID) != ""
}

// clientAddr is the visitor's address as the trust stage chose it.
func clientAddr(r *http.Request) string {
	if ip, ok := pipeline.ClientIP(r.Context()); ok {
		return ip.String()
	}
	return r.RemoteAddr
}
