package policydemo_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/switchboard-project/switchboard/internal/auth"
	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/policydemo"
)

func explain(t *testing.T, input string) (auth.Report, string) {
	t.Helper()
	out, err := policydemo.Explain([]byte(input))
	if err != nil {
		return auth.Report{}, err.Error()
	}
	var report auth.Report
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("not a report: %v\n%s", err, out)
	}
	return report, ""
}

const wiki = `"route":{"host":"Wiki.Acme.dev","auth_required":true,"access":{"required_groups":["engineering"]}}`

func TestTheDemoAnswersWhatTheEdgeAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		input   string
		allowed bool
		code    string
	}{
		"no session":      {`{` + wiki + `}`, false, "authentication_required"},
		"missing group":   {`{` + wiki + `,"principal":{"email":"bob@acme.dev","email_verified":true,"groups":["support"]}}`, false, "missing_required_group"},
		"in the group":    {`{` + wiki + `,"principal":{"email":"ana@acme.dev","email_verified":true,"groups":["engineering"]}}`, true, "allowed"},
		"public route":    {`{"route":{"host":"blog.acme.dev"}}`, true, "allowed"},
		"group is exact":  {`{` + wiki + `,"principal":{"email":"ana@acme.dev","email_verified":true,"groups":["Engineering"]}}`, false, "missing_required_group"},
		"unverified mail": {`{"route":{"host":"x.acme.dev","auth_required":true,"access":{"allowed_domains":["acme.dev"]}},"principal":{"email":"ana@acme.dev","email_verified":false,"groups":[]}}`, false, "email_not_verified"},
	} {
		t.Run(name, func(t *testing.T) {
			report, failure := explain(t, tc.input)
			if failure != "" {
				t.Fatal(failure)
			}
			if report.Decision.Allowed != tc.allowed || report.Decision.Code != tc.code {
				t.Fatalf("decision = %+v, want allowed=%v code=%s", report.Decision, tc.allowed, tc.code)
			}
		})
	}
}

// The demo must be the engine, not a copy of it: for the same route and
// principal it returns exactly what auth.NewReport returns.
func TestTheDemoIsTheEnginesOwnReport(t *testing.T) {
	route := config.Route{Host: "wiki.acme.dev", AuthRequired: true, Access: &config.AccessPolicy{RequiredGroups: []string{"engineering"}, MaxAuthAgeMinutes: 15}}
	principal := auth.Principal{Email: "ana@acme.dev", EmailVerified: true, Groups: []string{"engineering"}, Method: auth.MethodPasskey}
	want, _ := json.Marshal(auth.NewReport("wiki.acme.dev", route, &principal))
	got, err := policydemo.Explain([]byte(`{"route":{"host":"wiki.acme.dev","auth_required":true,"access":{"required_groups":["engineering"],"max_auth_age_minutes":15}},"principal":{"email":"ana@acme.dev","email_verified":true,"groups":["engineering"],"method":"passkey"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal(got, &b)
	// The simulated context carries a clock; compare what decides.
	delete(a, "context")
	delete(b, "context")
	wantJSON, _ := json.Marshal(a)
	gotJSON, _ := json.Marshal(b)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("the demo drifted from the engine:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}

func TestBadInputIsRefusedWithAReason(t *testing.T) {
	for name, tc := range map[string]struct{ input, mentions string }{
		"not json":        {`{`, "JSON"},
		"no host":         {`{"route":{}}`, "host"},
		"bad host":        {`{"route":{"host":"not a host!"}}`, "host"},
		"empty email":     {`{` + wiki + `,"principal":{"email":"","groups":[]}}`, "email"},
		"unknown method":  {`{` + wiki + `,"principal":{"email":"a@b.dev","method":"carrier-pigeon"}}`, "method"},
		"bound w/o pass.": {`{` + wiki + `,"principal":{"email":"a@b.dev","method":"oidc","device_bound":true}}`, "device_bound"},
		"too many groups": {`{` + wiki + `,"principal":{"email":"a@b.dev","groups":["` + strings.Repeat(`g","`, 70) + `g"]}}`, "groups"},
		"huge input":      {`{"route":{"host":"` + strings.Repeat("a", 70_000) + `"}}`, "large"},
	} {
		t.Run(name, func(t *testing.T) {
			_, failure := explain(t, tc.input)
			if failure == "" || !strings.Contains(failure, tc.mentions) {
				t.Fatalf("failure = %q, want one that mentions %q", failure, tc.mentions)
			}
		})
	}
}

func TestADemoRouteCannotReachAnything(t *testing.T) {
	// The route has no target and no network is involved: only the decision.
	report, failure := explain(t, `{"route":{"host":"x.acme.dev","target":"http://169.254.169.254/","auth_required":true,"access":{"allowed_emails":["a@b.dev"]}},"principal":{"email":"a@b.dev","email_verified":true,"groups":[]}}`)
	if failure != "" || !report.Decision.Allowed {
		t.Fatalf("report = %+v, failure = %q", report.Decision, failure)
	}
}

// The demo takes JSON typed into a web page. Whatever it is, the answer is
// either a refusal with a reason or a report that parses; it never panics, and
// it never answers with both.
func FuzzExplain(f *testing.F) {
	for _, seed := range []string{
		``, `{`, `null`, `[]`, `{"route":{}}`,
		`{"route":{"host":"a.dev"}}`,
		`{"route":{"host":"a.dev","auth_required":true,"access":{"allowed_domains":["x.dev"]}},"principal":{"email":"a@x.dev","email_verified":true}}`,
		`{"route":{"host":"a.dev","auth_required":true,"access":{"context_rule":{"when":{"session_device_bound":false},"then":"deny"}}},"principal":{"email":"a@x.dev","method":"passkey","device_bound":true}}`,
		`{"route":{"host":"a.dev"},"principal":{"email":"a@x.dev","groups":null,"method":"oidc","device_bound":true}}`,
		`{"route":{"host":"a.dev"},"request_method":"BREW"}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		out, err := policydemo.Explain(input)
		if (err == nil) == (out == nil) {
			t.Fatalf("exactly one of an answer and an error: err=%v out=%q", err, out)
		}
		if err == nil {
			var report map[string]any
			if json.Unmarshal(out, &report) != nil || report["decision"] == nil {
				t.Fatalf("the answer is not a report: %q", out)
			}
		}
	})
}
