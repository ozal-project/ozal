package passkey_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/switchboard-project/switchboard/internal/auth"
	"github.com/switchboard-project/switchboard/internal/config"
)

func route(host string, policy *config.AccessPolicy) config.Route {
	return config.Route{Host: host, Target: "http://127.0.0.1:9000", AuthRequired: policy != nil, Access: policy}
}

// signedInPortal enrols Ana (group ops) and returns her browser.
func signedInPortal(t *testing.T, h *harness) *browser {
	t.Helper()
	b := h.browser()
	if code := b.enrol(t, newAuthenticator(t), h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	return b
}

func (b *browser) portal(t *testing.T) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return b.post(t, "/_/auth/me/data", map[string]any{})
}

func appHosts(result map[string]any) []string {
	var hosts []string
	apps, _ := result["apps"].([]any)
	for _, a := range apps {
		hosts = append(hosts, a.(map[string]any)["host"].(string))
	}
	return hosts
}

func TestPortalListsOnlyWhatThePolicyAllows(t *testing.T) {
	h := newHarness(t)
	h.gate.WithApps(func() []config.Route {
		return []config.Route{
			route("wiki.example.test", &config.AccessPolicy{RequiredGroups: []string{"ops"}}),
			route("billing.example.test", &config.AccessPolicy{RequiredGroups: []string{"finance"}}),
			route("grafana.example.test", &config.AccessPolicy{AllowedDomains: []string{"other.org"}}),
			route("blog.example.test", nil), // public: not behind the login, not a launcher entry
			route("admin.example.test", &config.AccessPolicy{AllowedEmails: []string{"ana@example.com"}}),
		}
	})
	b := signedInPortal(t, h)
	rec, result := b.portal(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("portal = %d %s", rec.Code, rec.Body)
	}
	if result["email"] != "ana@example.com" || result["method"] != auth.MethodPasskey || result["passkeys"] != true {
		t.Fatalf("who = %v", result)
	}
	got := strings.Join(appHosts(result), ",")
	if got != "admin.example.test,wiki.example.test" {
		t.Fatalf("apps = %s; want only the routes Ana's identity and groups allow, sorted", got)
	}
	// Nothing about the routes she cannot open, or about any policy, reaches her.
	for _, secret := range []string{"billing", "finance", "grafana", "blog", "required_groups", "other.org", "allowed_"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("the portal leaked %q: %s", secret, rec.Body)
		}
	}
}

func TestPortalFollowsThePolicyInForceNotTheOneAtStartup(t *testing.T) {
	h := newHarness(t)
	policy := &config.AccessPolicy{RequiredGroups: []string{"ops"}}
	h.gate.WithApps(func() []config.Route { return []config.Route{route("wiki.example.test", policy)} })
	b := signedInPortal(t, h)
	if _, result := b.portal(t); len(appHosts(result)) != 1 {
		t.Fatalf("before: %v", result)
	}
	// A reload that changes only a policy does not rebuild the gate.
	policy = &config.AccessPolicy{RequiredGroups: []string{"finance"}}
	if _, result := b.portal(t); len(appHosts(result)) != 0 {
		t.Fatalf("after the policy changed, the portal still listed %v", appHosts(result))
	}
}

func TestPortalNotesWhatMayAskForAFreshLogin(t *testing.T) {
	h := newHarness(t)
	h.gate.WithApps(func() []config.Route {
		return []config.Route{
			route("plain.example.test", &config.AccessPolicy{RequiredGroups: []string{"ops"}}),
			route("strict.example.test", &config.AccessPolicy{RequiredGroups: []string{"ops"}, MaxAuthAgeMinutes: 15}),
		}
	})
	_, result := signedInPortal(t, h).portal(t)
	notes := map[string]string{}
	for _, a := range result["apps"].([]any) {
		app := a.(map[string]any)
		notes[app["host"].(string)], _ = app["note"].(string)
	}
	if notes["plain.example.test"] != "" || notes["strict.example.test"] == "" {
		t.Fatalf("notes = %v", notes)
	}
	if urls := result["apps"].([]any)[0].(map[string]any)["url"]; urls != "https://plain.example.test/" {
		t.Fatalf("url = %v", urls)
	}
}

func TestPortalNeedsASession(t *testing.T) {
	h := newHarness(t)
	h.gate.WithApps(func() []config.Route {
		return []config.Route{route("wiki.example.test", &config.AccessPolicy{RequiredGroups: []string{"ops"}})}
	})
	rec, _ := h.browser().portal(t)
	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "wiki") {
		t.Fatalf("anonymous data: %d %s", rec.Code, rec.Body)
	}

	page := httptest.NewRecorder()
	h.gate.ServeAuthEndpoint(page, httptest.NewRequest(http.MethodGet, loginOrigin+"/_/auth/me", nil))
	want := loginOrigin + "/_/auth/login?return_to=https%3A%2F%2Fauth.example.test%2F_%2Fauth%2Fme"
	if page.Code != http.StatusFound || page.Header().Get("Location") != want {
		t.Fatalf("anonymous page: %d %q", page.Code, page.Header().Get("Location"))
	}

	// The data endpoint obeys the same cross-site rule as every login API.
	b := signedInPortal(t, h)
	b.origin = "https://evil.example"
	if rec, _ := b.portal(t); rec.Code != http.StatusForbidden {
		t.Fatalf("data from another origin: %d", rec.Code)
	}
}

func TestPortalPageIsServedToASignedInPerson(t *testing.T) {
	h := newHarness(t)
	b := signedInPortal(t, h)
	req := httptest.NewRequest(http.MethodGet, loginOrigin+"/_/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: b.cookies[auth.SessionCookieName]})
	rec := httptest.NewRecorder()
	h.gate.ServeAuthEndpoint(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-page="me"`) {
		t.Fatalf("page: %d", rec.Code)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q", csp)
	}
}

func TestPortalIsClosedToADisabledPerson(t *testing.T) {
	h := newHarness(t)
	h.gate.WithApps(func() []config.Route {
		return []config.Route{route("wiki.example.test", &config.AccessPolicy{RequiredGroups: []string{"ops"}})}
	})
	b := signedInPortal(t, h)
	if err := h.identity.SetDisabled(t.Context(), "ana@example.com", true); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.portal(t); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled person still reads the portal: %d", rec.Code)
	}
}
