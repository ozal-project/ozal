package admin

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
)

// localKey marks a request that arrived on the admin unix socket (CLI-001).
type localKey struct{}

// peerKey holds the kernel-reported uid of the socket's peer, when known.
type peerKey struct{}

// LocalConn is an http.Server.ConnContext for the admin listeners: it marks
// connections accepted on a unix socket. The socket's file mode is the
// credential, so only those connections act as the host operator; a TCP
// connection never does, whatever its address. The peer's uid comes from the
// kernel (SO_PEERCRED), never from the request, and only names the actor.
func LocalConn(ctx context.Context, c net.Conn) context.Context {
	unixConn, ok := c.(*net.UnixConn)
	if !ok {
		return ctx
	}
	ctx = context.WithValue(ctx, localKey{}, true)
	if uid, ok := peerUID(unixConn); ok {
		ctx = context.WithValue(ctx, peerKey{}, uid)
	}
	return ctx
}

// isLocal reports whether r arrived on the admin unix socket.
func isLocal(r *http.Request) bool {
	local, _ := r.Context().Value(localKey{}).(bool)
	return local
}

// localActor names a socket request. The service account itself (the
// `sudo -u` path) stays "local"; another account reaching the socket through
// admin.socket_group is named, so the audit says which person acted.
func localActor(r *http.Request) string {
	uid, ok := r.Context().Value(peerKey{}).(int)
	if !ok || uid == os.Getuid() {
		return "local"
	}
	if account, err := user.LookupId(strconv.Itoa(uid)); err == nil && account.Username != "" {
		return "local:" + account.Username
	}
	return "local:uid=" + strconv.Itoa(uid)
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
