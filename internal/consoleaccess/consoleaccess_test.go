package consoleaccess

import (
	"strings"
	"testing"
)

const console = "https://console.example.com"

func TestDecide(t *testing.T) {
	cases := []struct {
		name    string
		req     Request
		allowed bool
		code    string
	}{
		{"plain http is refused before anything", Request{Method: "GET", Path: "/", Session: true}, false, CodeNotHTTPS},
		{"console page", Request{Method: "GET", Path: "/", HTTPS: true}, true, CodeStatic},
		{"console client route", Request{Method: "GET", Path: "/administrators", HTTPS: true}, true, CodeStatic},
		{"console asset", Request{Method: "GET", Path: "/assets/index-abc.js", HTTPS: true}, true, CodeStatic},
		{"sign-in settings", Request{Method: "GET", Path: "/_/api/admin/state", HTTPS: true}, true, CodeSignIn},
		{"stylesheet", Request{Method: "GET", Path: "/_/console.css", HTTPS: true}, true, CodeSignIn},
		{"start sign-in", Request{Method: "POST", Path: "/_/api/admin/remote/start", HTTPS: true, Origin: console, WantOrigin: console}, true, CodeSignIn},
		{"finish sign-in", Request{Method: "POST", Path: "/_/api/admin/remote/finish", HTTPS: true, Origin: console, WantOrigin: console}, true, CodeSignIn},
		{"resume", Request{Method: "POST", Path: "/_/api/operators/resume", HTTPS: true, Origin: console, WantOrigin: console}, true, CodeSignIn},
		{"sign-in from another origin", Request{Method: "POST", Path: "/_/api/admin/remote/start", HTTPS: true, Origin: "https://app.example.com", WantOrigin: console}, false, CodeCrossOrigin},
		{"sign-in without origin", Request{Method: "POST", Path: "/_/api/admin/remote/finish", HTTPS: true, WantOrigin: console}, false, CodeCrossOrigin},
		{"api read without session", Request{Method: "GET", Path: "/_/api/status", HTTPS: true}, false, CodeSignInRequired},
		{"debug view without session", Request{Method: "GET", Path: "/_/status", HTTPS: true}, false, CodeSignInRequired},
		{"event stream without session", Request{Method: "GET", Path: "/_/events", HTTPS: true}, false, CodeSignInRequired},
		{"health probe is not public here", Request{Method: "GET", Path: "/_/healthz", HTTPS: true}, false, CodeSignInRequired},
		{"route mutation without session", Request{Method: "POST", Path: "/routes", HTTPS: true, Origin: console, WantOrigin: console}, false, CodeSignInRequired},
		{"api read with session", Request{Method: "GET", Path: "/_/api/status", HTTPS: true, Session: true}, true, CodeSession},
		{"api change with session", Request{Method: "POST", Path: "/_/api/apps", HTTPS: true, Session: true, Origin: console, WantOrigin: console}, true, CodeSession},
		{"api change with session from a sibling", Request{Method: "POST", Path: "/_/api/apps", HTTPS: true, Session: true, Origin: "https://app.example.com", WantOrigin: console}, false, CodeCrossOrigin},
		{"api delete with session without origin", Request{Method: "DELETE", Path: "/routes/a.example.com", HTTPS: true, Session: true, WantOrigin: console}, false, CodeCrossOrigin},
		{"link enrolment", Request{Method: "POST", Path: "/_/api/admin/enrol/begin", HTTPS: true, Session: true, Origin: console, WantOrigin: console}, false, CodeHostOnly},
		{"loopback ceremony", Request{Method: "POST", Path: "/_/api/admin/login/begin", HTTPS: true, Session: true, Origin: console, WantOrigin: console}, false, CodeHostOnly},
		{"host login exchange", Request{Method: "POST", Path: "/_/api/operators/exchange", HTTPS: true, Session: true, Origin: console, WantOrigin: console}, false, CodeHostOnly},
		{"access request", Request{Method: "POST", Path: "/_/api/admin/requests", HTTPS: true, Origin: console, WantOrigin: console}, false, CodeHostOnly},
		{"access request status", Request{Method: "POST", Path: "/_/api/admin/requests/status", HTTPS: true, Origin: console, WantOrigin: console}, false, CodeHostOnly},
		{"enrolment codes", Request{Method: "GET", Path: "/_/api/admin/enrolments/code/x", HTTPS: true, Session: true}, false, CodeHostOnly},
		{"administrators manage requests", Request{Method: "GET", Path: "/_/api/admins/requests", HTTPS: true, Session: true}, true, CodeSession},
		{"dot segments", Request{Method: "GET", Path: "/assets/../_/api/status", HTTPS: true}, false, CodeInvalidPath},
		{"double slash", Request{Method: "GET", Path: "//_/api/status", HTTPS: true}, false, CodeInvalidPath},
		{"relative path", Request{Method: "GET", Path: "_/api/status", HTTPS: true}, false, CodeInvalidPath},
		{"empty want origin never matches", Request{Method: "POST", Path: "/_/api/admin/remote/start", HTTPS: true}, false, CodeCrossOrigin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.req)
			if got.Allowed != tc.allowed || got.Code != tc.code {
				t.Fatalf("Decide = %+v, want allowed=%v code=%s", got, tc.allowed, tc.code)
			}
			if got.Reason == "" {
				t.Fatal("a decision needs a reason")
			}
		})
	}
}

// The invariants hold for any input: nothing is answered over plain HTTP, no
// change is accepted from another origin, the host-only endpoints never
// answer, and without a session only the sign-in surface and console files do.
func FuzzDecide(f *testing.F) {
	f.Add("GET", "/", true, console, false)
	f.Add("POST", "/_/api/admin/remote/start", true, console, false)
	f.Add("POST", "/_/api/apps", true, "https://app.example.com", true)
	f.Add("DELETE", "/_/api/admin/enrol/x", true, console, true)
	f.Add("GET", "/_/api/status", false, "", true)
	f.Fuzz(func(t *testing.T, method, path string, https bool, origin string, session bool) {
		d := Decide(Request{Method: method, Path: path, HTTPS: https, Origin: origin, WantOrigin: console, Session: session})
		if !d.Allowed {
			return
		}
		if !https {
			t.Fatal("allowed over plain HTTP")
		}
		if method != "GET" && method != "HEAD" && origin != console {
			t.Fatalf("allowed a %s from %q", method, origin)
		}
		for _, prefix := range hostOnly {
			if strings.HasPrefix(path, prefix) {
				t.Fatalf("allowed host-only %s", path)
			}
		}
		if !session {
			_, signInPath := signIn[method+" "+path]
			static := (method == "GET" || method == "HEAD") && !strings.HasPrefix(path, controlPlanePrefix)
			if !signInPath && !static {
				t.Fatalf("allowed %s %s without a session", method, path)
			}
		}
	})
}
