package pipeline

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/policycontext"
)

func TestRequestPolicyContextRecordsProvenanceWithoutRawRequestData(t *testing.T) {
	observedAt := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	request := httptest.NewRequest(http.MethodGet, "https://admin.example.com/_/auth/callback?code=never-log-this", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("CF-Connecting-IP", "198.51.100.24")
	request.Header.Set("Cookie", "sb_session=secret-value")
	request.Header.Set(IdentityUserHeader, "attacker-controlled")
	trusted := netip.MustParsePrefix("127.0.0.0/8")
	principal := &auth.Principal{Method: auth.MethodPasskey, AuthTime: observedAt.Add(-time.Minute), DeviceBound: false}

	var got policycontext.Context
	TrustWithProxies([]netip.Prefix{trusted})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RequestPolicyContext(r, "admin.example.com", principal, observedAt, nil)
	})).ServeHTTP(httptest.NewRecorder(), request)

	if got.Network.ArrivalPath.Value == nil || *got.Network.ArrivalPath.Value != policycontext.ArrivalProxy {
		t.Fatalf("arrival path = %+v", got.Network.ArrivalPath)
	}
	if got.Network.VisitorIPSource.Value == nil || *got.Network.VisitorIPSource.Value != policycontext.IPSourceProxy {
		t.Fatalf("visitor IP source = %+v", got.Network.VisitorIPSource)
	}
	if got.Action.PathClass.Value == nil || *got.Action.PathClass.Value != policycontext.PathAuthentication {
		t.Fatalf("path class = %+v", got.Action.PathClass)
	}
	if got.Session.UserVerified.Value != nil {
		t.Fatalf("user verification was inferred without a stored signal: %+v", got.Session.UserVerified)
	}
	if got.Session.DeviceBound.Value == nil || *got.Session.DeviceBound.Value {
		t.Fatalf("known unbound device was lost: %+v", got.Session.DeviceBound)
	}
	if got.Resource.Owner.Value != nil || got.Resource.DataClass.Value != nil || got.Resource.Sensitivity.Value != nil {
		t.Fatalf("missing route catalog was inferred: %+v", got.Resource)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal request context: %v", err)
	}
	for _, secret := range []string{"/_/auth/callback", "never-log-this", "198.51.100.24", "secret-value", "attacker-controlled"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("request context leaked %q: %s", secret, data)
		}
	}
}

func TestRequestPolicyContextCarriesWebAuthnUserVerification(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(strconv.FormatBool(verified), func(t *testing.T) {
			firstSeen := true
			principal := &auth.Principal{Method: auth.MethodPasskey, UserVerified: &verified, DeviceFirstSeen: &firstSeen}
			context := RequestPolicyContext(nil, "app.example.com", principal, time.Now().UTC(), nil)
			if got := context.Session.UserVerified; got.Value == nil || *got.Value != verified || got.Source != policycontext.SourceSession {
				t.Fatalf("session.user_verified = %+v, want %t from session", got, verified)
			}
			if got := context.History.DeviceFirstSeen; got.Value == nil || !*got.Value || got.Source != policycontext.SourceSession {
				t.Fatalf("history.device_first_seen = %+v, want true from session", got)
			}
		})
	}
	untrusted := true
	principal := &auth.Principal{Method: auth.MethodOIDC, UserVerified: &untrusted, DeviceFirstSeen: &untrusted}
	context := RequestPolicyContext(nil, "app.example.com", principal, time.Now().UTC(), nil)
	if got := context.Session.UserVerified; got.Value != nil || got.Source != policycontext.SourceNotRecorded {
		t.Fatalf("OIDC user verification was inferred: %+v", got)
	}
	if got := context.History.DeviceFirstSeen; got.Value != nil || got.Source != policycontext.SourceNotRecorded {
		t.Fatalf("OIDC device history was inferred: %+v", got)
	}
}

func TestRequestPolicyContextMarksTunnelAssertionSource(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://app.example.com/write", nil)
	request.RemoteAddr = "10.0.0.2:443"
	request.Header.Set(VisitorIPHeader, "203.0.113.9")
	var got policycontext.Context
	TrustAuthenticatedTunnel()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RequestPolicyContext(r, "app.example.com", nil, time.Now().UTC(), nil)
	})).ServeHTTP(httptest.NewRecorder(), request)
	if value := got.Network.ArrivalPath.Value; value == nil || *value != policycontext.ArrivalTunnel {
		t.Fatalf("arrival path = %+v", got.Network.ArrivalPath)
	}
	if value := got.Network.VisitorIPSource.Value; value == nil || *value != policycontext.IPSourceTunnel {
		t.Fatalf("visitor IP source = %+v", got.Network.VisitorIPSource)
	}
}

func TestRequestPolicyContextDoesNotGuessNetworkSourceWithoutPeerEvidence(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	request.RemoteAddr = "unparseable"
	var got policycontext.Context
	TrustWithProxies(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RequestPolicyContext(r, "app.example.com", nil, time.Now().UTC(), nil)
	})).ServeHTTP(httptest.NewRecorder(), request)
	if got.Network.ArrivalPath.Value != nil || got.Network.VisitorIPSource.Value != nil {
		t.Fatalf("invalid peer was turned into a known network source: %+v", got.Network)
	}
}

func TestRequestPolicyContextUsesDeclaredResourceLabelsAndRiskyActions(t *testing.T) {
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	catalog := &config.RouteResource{
		Owner: "platform", DataClass: "account_data", Sensitivity: "restricted",
		RiskyActions: []string{"POST", "DELETE"},
	}
	request := httptest.NewRequest(http.MethodPost, "https://app.example.com/action?secret=never-record", nil)
	got := RequestPolicyContext(request, "app.example.com", nil, at, catalog)
	if got.Resource.Owner.Value == nil || *got.Resource.Owner.Value != "platform" || got.Resource.Owner.Source != policycontext.SourceRouteConfig {
		t.Fatalf("owner evidence = %+v", got.Resource.Owner)
	}
	if got.Resource.DataClass.Value == nil || *got.Resource.DataClass.Value != "account_data" || got.Resource.Sensitivity.Value == nil || *got.Resource.Sensitivity.Value != "restricted" {
		t.Fatalf("resource labels = %+v", got.Resource)
	}
	if got.Action.Risky.Value == nil || !*got.Action.Risky.Value || got.Action.Risky.Source != policycontext.SourceRouteConfig {
		t.Fatalf("POST should be a configured risky action: %+v", got.Action.Risky)
	}

	request.Method = http.MethodGet
	got = RequestPolicyContext(request, "app.example.com", nil, at, catalog)
	if got.Action.Risky.Value == nil || *got.Action.Risky.Value {
		t.Fatalf("GET should be a configured non-risky action: %+v", got.Action.Risky)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "never-record") || strings.Contains(string(data), "/action") {
		t.Fatalf("resource context included a raw request URL: %s", data)
	}
}

func TestClassifyRequestPathUsesSegmentBoundaries(t *testing.T) {
	for path, want := range map[string]string{
		"/_/auth/callback": policycontext.PathAuthentication,
		"/_/api/users":     policycontext.PathControlAPI,
		"/_/status":        policycontext.PathControlPage,
		"/_/authentic":     policycontext.PathControlPage,
		"/admin":           policycontext.PathApplication,
	} {
		if got := classifyRequestPath(path); got != want {
			t.Errorf("classifyRequestPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func FuzzRequestPolicyContextNoPanic(f *testing.F) {
	f.Add("GET", "/_/auth/callback", "token=secret", "X-Forwarded-For", "198.51.100.9")
	f.Add("\x00\r\n", "//evil/../path", "q=1", "bad\x00header", "")

	f.Fuzz(func(t *testing.T, method, path, query, headerName, headerValue string) {
		header := make(http.Header)
		header[headerName] = []string{headerValue}
		header["CF-Connecting-IP"] = []string{"198.51.100.1"}
		request := &http.Request{
			Method:     method,
			URL:        &url.URL{Path: path, RawQuery: query},
			Host:       "fuzz.example.test",
			Header:     header,
			RemoteAddr: "not an address",
		}
		var got policycontext.Context
		TrustWithProxies(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = RequestPolicyContext(r, "fuzz.example.test", nil, time.Unix(0, 0).UTC(), nil)
		})).ServeHTTP(httptest.NewRecorder(), request)
		if _, err := json.Marshal(got); err != nil {
			t.Fatalf("marshal hostile request context: %v", err)
		}
	})
}
