package admin

import (
	"encoding/json"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHostOperatorLoginIsSingleUseScopedAndRevocable(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "global", SitesToken: "deployer"}}}
	recorder := &events.Recorder{}
	handler := New(manager, recorder)
	for _, token := range []string{"", "global", "deployer"} {
		if rec := request(handler, http.MethodPost, "/_/api/operators", token, `{"name":"Ana"}`); rec.Code != 403 {
			t.Fatalf("TCP issued host credential: %d", rec.Code)
		}
	}
	code, _, err := handler.operators.issue("Ana", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := handler.operators.authenticate(code); ok {
		t.Fatal("one-use code acted as admin bearer")
	}
	session, g, err := handler.operators.exchange(code)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := handler.operators.exchange(code); err == nil {
		t.Fatal("host login replayed")
	}
	if rec := request(handler, http.MethodPost, "/_/reload", session, ""); rec.Code != 200 {
		t.Fatalf("operator mutation: %d %s", rec.Code, rec.Body)
	}
	if got := recorder.Events(); got[len(got)-1].Actor != "operator:Ana" {
		t.Fatal("operator attribution lost")
	}
	if rec := request(handler, http.MethodPost, "/_/api/people/invite", session, `{"email":"a@example.com","recover":true}`); rec.Code != 404 {
		t.Fatalf("unexpected people endpoint without directory: %d", rec.Code)
	}
	if rec := request(handler, http.MethodPost, "/_/api/operators", session, `{"name":"Eve"}`); rec.Code != 403 {
		t.Fatal("operator issued another credential")
	}
	handler.operators.revoke(g.ID)
	if rec := request(handler, http.MethodPost, "/_/reload", session, ""); rec.Code != 401 {
		t.Fatal("revoked operator session worked")
	}
}

func TestOperatorExchangeOriginExpiryAndCapacity(t *testing.T) {
	handler := New(&fakeManager{}, nil)
	code, _, err := handler.operators.issue("Ana", true)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost/_/api/operators/exchange", strings.NewReader(`{"token":"`+code+`"}`))
	req.Header.Set("Origin", "http://other.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatal("cross-origin exchange accepted")
	}
	now := time.Now()
	handler.operators.now = func() time.Time { return now.Add(6 * time.Minute) }
	if _, _, err := handler.operators.exchange(code); err == nil {
		t.Fatal("expired bootstrap accepted")
	}
	handler.operators.now = func() time.Time { return now }
	for i := 0; i < 64; i++ {
		if _, _, err := handler.operators.issue("Ana", true); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := handler.operators.issue("Ana", true); err == nil {
		t.Fatal("unbounded operator store")
	}
}

func TestOperatorHTTPExchangePreservesIDAndActiveTokenExpires(t *testing.T) {
	handler := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: "global"}}}, nil)
	now := time.Now()
	handler.operators.now = func() time.Time { return now }
	code, issued, err := handler.operators.issue("Ana", true)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost/_/api/operators/exchange", strings.NewReader(`{"token":"`+code+`"}`))
	req.Header.Set("Origin", "http://localhost")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("exchange: %d %s", rec.Code, rec.Body)
	}
	var response struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != issued.ID {
		t.Fatal("issued ID cannot revoke an exchanged session")
	}
	if _, ok := handler.operators.authenticate(response.Token); !ok {
		t.Fatal("active session refused")
	}
	handler.operators.now = func() time.Time { return now.Add(16 * time.Minute) }
	if _, ok := handler.operators.authenticate(response.Token); ok {
		t.Fatal("expired active session accepted")
	}
}

func TestTemporaryOperatorCannotPublishHostSecretsThroughEitherRouteAPI(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Listen: "127.0.0.1:8888", Admin: config.Admin{Token: "global"}, Routes: []config.Route{{Host: "app.localhost", Target: "http://127.0.0.1:3000"}}}}
	handler := New(manager, nil)
	token, _, err := handler.operators.issue("Ana", false)
	if err != nil {
		t.Fatal(err)
	}
	route := `{"host":"secrets.localhost","root":"/etc/switchboard"}`
	if rec := request(handler, http.MethodPost, "/routes", token, route); rec.Code != 403 {
		t.Fatalf("legacy API exposed host root: %d", rec.Code)
	}
	for _, path := range []string{"/_/api/apps/plan", "/_/api/apps"} {
		body := `{"route":` + route + `,"expected":"` + manager.ConfigDigest() + `","access_mode":"public"}`
		if rec := request(handler, http.MethodPost, path, token, body); rec.Code != 403 {
			t.Fatalf("%s exposed host root: %d", path, rec.Code)
		}
	}
	if len(manager.added) != 0 {
		t.Fatal("denied filesystem root reached the route manager")
	}
}
