package passkey

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
)

// The portal (ID-012) is what a person who uses the applications sees: who
// they are signed in as and the applications they may open. It is computed
// from the same pure decision the proxy makes (auth.Explain), so it never
// lists an application the policy would refuse, and it never shows policies,
// other people's groups or routes that are not theirs.

const portalPath = "/_/auth/me"

// WithApps tells the portal where the current protected routes come from. It
// is a function, not a copy: the gate outlives a configuration reload that
// changes only policies, so the routes are read when the portal is opened.
func (g *Gate) WithApps(routes func() []config.Route) *Gate {
	g.apps = routes
	return g
}

type appView struct {
	Host string `json:"host"`
	URL  string `json:"url"`
	// Note is set when the route may ask for a fresh login even though the
	// policy allows this person.
	Note string `json:"note,omitempty"`
}

type meView struct {
	Email    string    `json:"email"`
	Method   string    `json:"method"`
	Apps     []appView `json:"apps"`
	Passkeys bool      `json:"passkeys"`
}

// openableApps are the protected routes the principal's identity policy
// allows. A step-up decision still counts as allowed: the door asks again.
func (g *Gate) openableApps(principal auth.Principal) []appView {
	if g.apps == nil {
		return nil
	}
	scheme := "https"
	if login, err := url.Parse(g.origin); err == nil && login.Scheme != "" {
		scheme = login.Scheme
	}
	apps := []appView{}
	for _, route := range g.apps() {
		if !route.RequiresAuth() {
			continue
		}
		if !auth.Explain(route, &principal).Decision.Allowed {
			continue
		}
		app := appView{Host: route.Host, URL: scheme + "://" + route.Host + "/"}
		if policy := route.Policy(); policy.MaxAuthAgeMinutes > 0 || policy.RequireDeviceBound || len(policy.Methods) > 0 {
			app.Note = "May ask you to sign in again"
		}
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool { return strings.Compare(apps[i].Host, apps[j].Host) < 0 })
	return apps
}

// serveMePage shows the portal to any signed-in person; anyone else signs in
// first and comes back.
func (g *Gate) serveMePage(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.Authenticate(r); !ok && r.Method == http.MethodGet {
		http.Redirect(w, r, g.origin+"/_/auth/login?return_to="+url.QueryEscape(g.origin+portalPath), http.StatusFound)
		return
	}
	g.servePage(w, r)
}

func (g *Gate) meData(_ http.ResponseWriter, r *http.Request) (any, *apiError) {
	principal, ok := g.Authenticate(r)
	if !ok {
		return nil, &errSignedOut
	}
	return meView{
		Email:    principal.Email,
		Method:   principal.Method,
		Apps:     g.openableApps(principal),
		Passkeys: principal.Method == auth.MethodPasskey,
	}, nil
}
