package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
)

const explainToken = "explain-test-token"

func explainHandler() *Handler {
	return New(&fakeManager{cfg: config.Config{
		Admin: config.Admin{Token: explainToken},
		Routes: []config.Route{
			{Host: "www.example.com", Target: "http://127.0.0.1:18001"},
			{Host: "app.example.com", Target: "http://127.0.0.1:18002", Access: &config.AccessPolicy{
				AllowedEmails:  []string{"secret-person@example.com"},
				RequiredGroups: []string{"engineering"},
			}},
		},
	}}, nil)
}

// The explain API reveals allowlists and can be probed as an oracle, so it is
// held to the same bearer check as mutations.
func TestExplainRequiresBearer(t *testing.T) {
	body := `{"host":"app.example.com","principal":{"email":"secret-person@example.com","email_verified":true,"groups":["engineering"]}}`
	for _, token := range []string{"", "wrong-token"} {
		rec := request(explainHandler(), http.MethodPost, "/_/api/policy/explain", token, body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: status = %d; want 401", token, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "secret-person") {
			t.Errorf("token %q: unauthorized response leaks policy: %s", token, rec.Body)
		}
	}
}

func TestExplainAPI(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		status   int
		code     string
		scope    string
		protects bool
	}{
		{"allowed", `{"host":"app.example.com","principal":{"email":"Secret-Person@example.com","email_verified":true,"groups":["engineering"]}}`, 200, "allowed", auth.ScopeAuthorization, true},
		{"missing group", `{"host":"app.example.com","principal":{"email":"secret-person@example.com","email_verified":true,"groups":["Engineering"]}}`, 200, "missing_required_group", auth.ScopeAuthorization, true},
		{"no session stops at gate", `{"host":"APP.example.com.","principal":null}`, 200, "authentication_required", auth.ScopeGate, true},
		{"public route", `{"host":"www.example.com"}`, 200, "allowed", auth.ScopePublic, false},
		{"unknown host", `{"host":"nope.example.com","principal":null}`, 404, "", "", false},
		{"invalid host", `{"host":"","principal":null}`, 400, "", "", false},
		{"principal without email", `{"host":"app.example.com","principal":{"email_verified":true}}`, 400, "", "", false},
		{"unknown field", `{"host":"app.example.com","principal":null,"admin":true}`, 400, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := request(explainHandler(), http.MethodPost, "/_/api/policy/explain", explainToken, tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status = %d; want %d (%s)", rec.Code, tt.status, rec.Body)
			}
			if tt.status != http.StatusOK {
				return
			}
			var got auth.Report
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Decision.Code != tt.code || got.Scope != tt.scope || got.Protected != tt.protects {
				t.Errorf("report = %+v; want code=%s scope=%s protected=%t", got, tt.code, tt.scope, tt.protects)
			}
		})
	}
}

func TestSimulatorPageMovedToConsole(t *testing.T) {
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: explainToken}}}, nil, WithEvents(events.NewBroker()))
	rec := request(h, http.MethodGet, "/simulator", "", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), explainToken) {
		t.Fatalf("simulator: status %d; the page must never carry the admin token", rec.Code)
	}
	if old := request(h, http.MethodGet, "/_/simulator", "", ""); old.Code != http.StatusMovedPermanently || old.Header().Get("Location") != "/simulator" {
		t.Errorf("old simulator URL: %d %v", old.Code, old.Header())
	}
}
