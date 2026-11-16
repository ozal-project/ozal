// Package consoleaccess decides what the remote console host (admin.remote)
// answers. It is the pure core in front of every console handler on that
// host: without an administrator passkey session opened there, only the
// sign-in surface answers and everything else looks like a path that does not
// exist. The adapter in internal/admin gathers the inputs and carries out the
// decision; nothing here touches the network, the clock or storage.
package consoleaccess

import "strings"

// Request is what the decision needs from one request on the remote host.
type Request struct {
	Method string
	Path   string
	// HTTPS is the visitor's scheme as the trust stage decided it.
	HTTPS bool
	// Origin is the request's Origin header; WantOrigin is the console's own
	// origin, https://<admin.remote.host>.
	Origin, WantOrigin string
	// Session is true when the request carries a live administrator passkey
	// session that was opened on this remote host.
	Session bool
}

// Decision is the outcome with a stable code for tests, events and metrics.
type Decision struct {
	Allowed bool
	Code    string
	Reason  string
}

// Stable decision codes.
const (
	CodeNotHTTPS        = "remote.not_https"
	CodeCrossOrigin     = "remote.cross_origin"
	CodeHostOnly        = "remote.host_only"
	CodeSignInRequired  = "remote.sign_in_required"
	CodeStatic          = "remote.static"
	CodeSignIn          = "remote.sign_in"
	CodeSession         = "remote.session"
	CodeInvalidPath     = "remote.invalid_path"
	controlPlanePrefix  = "/_/"
	maxPathLength       = 2048
	signInStateEndpoint = "/_/api/admin/state"
)

// signIn is the whole pre-authentication API: the sign-in settings, the
// login-host round trip and resuming a session after a reload. Each handler
// checks the console origin itself as well.
var signIn = map[string]string{
	"GET " + signInStateEndpoint:      "the sign-in settings",
	"HEAD " + signInStateEndpoint:     "the sign-in settings",
	"POST /_/api/admin/remote/start":  "starting a sign-in on the login host",
	"POST /_/api/admin/remote/finish": "finishing a sign-in on the login host",
	"POST /_/api/operators/resume":    "resuming this browser's session",
	"GET /_/console.css":              "the console stylesheet",
	"GET /_/fonts/manrope.ttf":        "the console font",
	"HEAD /_/console.css":             "the console stylesheet",
	"HEAD /_/fonts/manrope.ttf":       "the console font",
}

// hostOnly are console endpoints that exist for the loopback console and must
// not answer on a public host even with a session: link-based enrolment and
// sign-in ceremonies bound to ozal.localhost, host logins issued by
// `ozal console login`, access requests from people without an account, and
// enrolment codes (typed on the login host, never here).
var hostOnly = []string{
	"/_/api/admin/enrol/",
	"/_/api/admin/login/",
	"/_/api/admin/requests",
	"/_/api/admin/enrolments",
	"/_/api/operators/exchange",
}

// Decide applies, in order: HTTPS, the exact console origin for anything that
// is not a read, the host-only endpoints, the public sign-in surface and
// static console files, and finally a session for everything else.
func Decide(req Request) Decision {
	if !req.HTTPS {
		return Decision{Code: CodeNotHTTPS, Reason: "the remote console answers only over HTTPS"}
	}
	path := req.Path
	if path == "" || path[0] != '/' || len(path) > maxPathLength || strings.Contains(path, "/../") || strings.HasSuffix(path, "/..") || strings.Contains(path, "//") {
		return Decision{Code: CodeInvalidPath, Reason: "not a console path"}
	}
	read := req.Method == "GET" || req.Method == "HEAD"
	if !read && (req.WantOrigin == "" || req.Origin != req.WantOrigin) {
		return Decision{Code: CodeCrossOrigin, Reason: "a change must come from the console's own page"}
	}
	for _, prefix := range hostOnly {
		if strings.HasPrefix(path, prefix) {
			return Decision{Code: CodeHostOnly, Reason: "this part of the console is available on the host only"}
		}
	}
	if what, ok := signIn[req.Method+" "+path]; ok {
		return Decision{Allowed: true, Code: CodeSignIn, Reason: what}
	}
	if read && !strings.HasPrefix(path, controlPlanePrefix) {
		// The console's own files: the same bundle every release ships.
		return Decision{Allowed: true, Code: CodeStatic, Reason: "a console file"}
	}
	if req.Session {
		return Decision{Allowed: true, Code: CodeSession, Reason: "an administrator passkey session"}
	}
	return Decision{Code: CodeSignInRequired, Reason: "sign in with an administrator passkey first"}
}
