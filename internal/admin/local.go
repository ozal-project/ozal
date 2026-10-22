package admin

import (
	"context"
	"net"
	"net/http"
)

// localKey marks a request that arrived on the admin unix socket (CLI-001).
type localKey struct{}

// LocalConn is an http.Server.ConnContext for the admin listeners: it marks
// connections accepted on a unix socket. The socket's file mode is the
// credential, so only those connections act as the host operator; a TCP
// connection never does, whatever its address.
func LocalConn(ctx context.Context, c net.Conn) context.Context {
	if _, ok := c.(*net.UnixConn); ok {
		return context.WithValue(ctx, localKey{}, true)
	}
	return ctx
}

// isLocal reports whether r arrived on the admin unix socket.
func isLocal(r *http.Request) bool {
	local, _ := r.Context().Value(localKey{}).(bool)
	return local
}

// requireLocal admits only requests from the admin unix socket: the
// host-only operations (account recovery, connector enrolment) that no
// bearer token may run. Any other request gets 403 with a JSON error that
// names the socket, and next is not called.
func (h *Handler) requireLocal(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLocal(r) {
			writeError(w, http.StatusForbidden, "host-only operation: use the admin unix socket (admin.socket) on the host")
			return
		}
		next(w, r)
	}
}
