package passkey_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/auth"
)

func denied(t testing.TB, h *harness, method, host string, p auth.Principal, code auth.DecisionCode) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "https://app.example.test/", nil)
	req.Host = host // as a Host header arrives: not parsed as part of a URL
	rec := httptest.NewRecorder()
	if !h.gate.ServeDenied(rec, req, p, code) {
		t.Fatal("the gate did not write the page")
	}
	return rec
}

func TestTheRefusalPageExplainsAndOffersTheWayBack(t *testing.T) {
	h := newHarness(t)
	rec := denied(t, h, http.MethodGet, appHost, auth.Principal{Email: "bob@other.org"}, auth.CodeDomainNotAllowed)
	body := rec.Body.String()
	if rec.Code != http.StatusForbidden || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{"You can't open", appHost, "bob@other.org", loginOrigin + "/_/auth/me", "See the apps you can open", "domain_not_allowed", "ask the person who runs it"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: CSP %q, cache %q", csp, rec.Header().Get("Cache-Control"))
	}
}

// Anyone who can sign in could read the policy from this page, so it states
// the person's own situation and nothing about what the policy lists.
func TestTheRefusalPageNeverReadsOutThePolicy(t *testing.T) {
	h := newHarness(t)
	for _, code := range []auth.DecisionCode{auth.CodeDomainNotAllowed, auth.CodeEmailNotAllowed, auth.CodeMissingRequiredGroup, auth.CodeEmailNotVerified, auth.CodeContextRuleDenied, auth.CodeDeviceBoundRequired} {
		body := denied(t, h, http.MethodGet, appHost, auth.Principal{Email: "bob@other.org"}, code).Body.String()
		for _, leak := range []string{"engineering", "finance", "acme.dev", "allowed_domains", "allowed_emails", "required_groups", "allowlist"} {
			if strings.Contains(strings.ToLower(body), leak) {
				t.Errorf("code %s: the page says %q", code, leak)
			}
		}
	}
}

func TestEverythingFromTheRequestAndTheSessionIsEscaped(t *testing.T) {
	h := newHarness(t)
	evil := `"><script>alert(1)</script>@x.dev`
	rec := denied(t, h, http.MethodGet, `evil"><img src=x onerror=alert(1)>.example.test`, auth.Principal{Email: evil}, auth.CodeDomainNotAllowed)
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") {
		t.Fatalf("unescaped markup reached the page:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("the address should still be shown, as text")
	}
}

// The template is filled in one pass: a value that looks like a placeholder is
// shown as text, not filled in.
func TestAValueThatLooksLikeAPlaceholderIsNotFilledIn(t *testing.T) {
	h := newHarness(t)
	body := denied(t, h, http.MethodGet, appHost, auth.Principal{Email: "{{why}}@x.dev"}, auth.CodeDomainNotAllowed).Body.String()
	if strings.Count(body, "That account does not have access to this app.") != 1 || !strings.Contains(body, "{{why}}@x.dev") {
		t.Fatalf("a placeholder in the address was interpreted:\n%s", body)
	}
}

func TestAHeadRequestGetsTheHeadersOnly(t *testing.T) {
	h := newHarness(t)
	rec := denied(t, h, http.MethodHead, appHost, auth.Principal{Email: "bob@other.org"}, auth.CodeDomainNotAllowed)
	if rec.Code != http.StatusForbidden || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: %d with %d body bytes", rec.Code, rec.Body.Len())
	}
}

func TestTheReasonTellsThePersonWhatTheyCanDo(t *testing.T) {
	h := newHarness(t)
	for code, want := range map[auth.DecisionCode]string{
		auth.CodeEmailNotVerified:      "has not confirmed this email address",
		auth.CodeDeviceBoundRequired:   "stays on one device",
		auth.CodeContextRuleDenied:     "extra requirement",
		auth.CodeMissingRequiredGroup:  "does not have access to this app",
		auth.DecisionCode("brand_new"): "does not have access to this app",
	} {
		if body := denied(t, h, http.MethodGet, appHost, auth.Principal{Email: "a@b.dev"}, code).Body.String(); !strings.Contains(body, want) {
			t.Errorf("code %s: no %q in the page", code, want)
		}
	}
}

// Whatever the host header and the session carry, the page has the markup the
// template wrote and not one tag more: every value stays text. The baseline
// is the same page for harmless values.
func FuzzTheRefusalPageNeverGrowsMarkup(f *testing.F) {
	for _, seed := range [][2]string{
		{"app.example.test", "ana@example.com"},
		{`"><script>alert(1)</script>`, `'><img src=x onerror=alert(1)>`},
		{"{{.Why}}", "{{template \"x\"}}"},
		{"a\x00b", "\u202e<b>"},
		{"</title><script>", "javascript:alert(1)"},
	} {
		f.Add(seed[0], seed[1])
	}
	h := newHarness(f)
	baseline := strings.Count(denied(f, h, http.MethodGet, "app.example.test", auth.Principal{Email: "a@b.dev"}, auth.CodeDomainNotAllowed).Body.String(), "<")
	f.Fuzz(func(t *testing.T, host, email string) {
		if len(host) > 1000 || len(email) > 1000 {
			return
		}
		body := denied(t, h, http.MethodGet, host, auth.Principal{Email: email}, auth.CodeDomainNotAllowed).Body.String()
		if got := strings.Count(body, "<"); got != baseline {
			t.Fatalf("%d tags opened, want %d (host %q, email %q)", got, baseline, host, email)
		}
	})
}
