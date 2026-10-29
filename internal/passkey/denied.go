package passkey

import (
	"html/template"
	"net/http"
	"sync"

	"github.com/ozal-project/ozal/internal/auth"
)

// deniedPage is parsed once. It is an html/template on purpose: everything the
// page shows came from a request or a session, and the template escapes each
// value for the place it lands in (text, attribute, URL), so no field relies on
// someone remembering to escape it.
var deniedPage = sync.OnceValues(func() (*template.Template, error) {
	page, err := assets.ReadFile("assets/denied.html")
	if err != nil {
		return nil, err
	}
	return template.New("denied").Parse(string(versioned(page)))
})

type deniedView struct {
	Host, Email, Why, Portal, Reference string
}

// ServeDenied answers a signed-in person whom policy refused with a page in
// place of a bare "access denied" (CTX-007): who they are signed in as, a
// plain reason, the way back to the apps they can open, and a reference the
// operator can find in the access log.
//
// The page explains the situation of that person and nothing more. It never
// names a group, a domain or an address the policy lists: anyone who can sign
// in could otherwise read who may.
func (g *Gate) ServeDenied(w http.ResponseWriter, r *http.Request, p auth.Principal, code auth.DecisionCode) bool {
	page, err := deniedPage()
	if err != nil {
		return false
	}
	pageHeaders(w.Header())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	if r.Method == http.MethodHead {
		return true
	}
	_ = page.Execute(w, deniedView{
		Host:      r.Host,
		Email:     p.Email,
		Why:       deniedReason(code),
		Portal:    g.origin + portalPath,
		Reference: string(code) + " · " + g.now().UTC().Format("2006-01-02 15:04 UTC"),
	})
	return true
}

// deniedReason says why in words the person can act on. Anything that would
// reveal what the policy lists is left out on purpose.
func deniedReason(code auth.DecisionCode) string {
	switch code {
	case auth.CodeEmailNotVerified:
		return "Your sign-in provider has not confirmed this email address, so it cannot be used here."
	case auth.CodeDeviceBoundRequired:
		return "This app only opens from a passkey that stays on one device."
	case auth.CodeContextRuleDenied, auth.CodeContextSignalUnknown:
		return "This app has an extra requirement that this sign-in does not meet."
	default:
		return "That account does not have access to this app."
	}
}
