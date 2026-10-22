package admin

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
	"github.com/octaviosantacruz/switchboard/internal/tunnel"
)

func connectorHandler(t *testing.T) (http.Handler, *events.Recorder) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	recorder := &events.Recorder{}
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	return New(manager, recorder, WithConnectors(tunnel.NewRegistry(db))), recorder
}

func certificatePEM(t *testing.T) (string, string) {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "c"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	fp := tunnel.FingerprintOf(der)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), hex.EncodeToString(fp[:])
}

func local(handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := localRequest(method, target)
	if body != "" {
		req = httptest.NewRequestWithContext(req.Context(), method, target, strings.NewReader(body))
		req.Host = "localhost"
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func jsonBody(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Whoever enrols a certificate decides which machine serves a protected
// host: no bearer may change the registry, the socket may, with an event.
func TestConnectorChangesAreHostOnly(t *testing.T) {
	handler, recorder := connectorHandler(t)
	cert, sum := certificatePEM(t)
	enrol := jsonBody(t, map[string]string{"name": "portfolio", "certificate_pem": cert})

	for _, c := range []struct{ method, target, body string }{
		{http.MethodPost, "/_/api/connectors", enrol},
		{http.MethodPost, "/_/api/connectors/portfolio/rotate", `{}`},
		{http.MethodPost, "/_/api/connectors/portfolio/revoke", ""},
	} {
		if rec := request(handler, c.method, c.target, "secret", c.body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s with the admin token = %d, want 403", c.method, c.target, rec.Code)
		}
	}
	if len(recorder.Events()) != 0 {
		t.Fatalf("refused changes emitted %+v", recorder.Events())
	}

	if rec := local(handler, http.MethodPost, "/_/api/connectors", enrol); rec.Code != http.StatusCreated {
		t.Fatalf("enrol on the socket = %d %s", rec.Code, rec.Body)
	}
	if rec := local(handler, http.MethodPost, "/_/api/connectors", enrol); rec.Code != http.StatusConflict {
		t.Fatalf("second enrol = %d, want 409", rec.Code)
	}
	// Reads take the admin token.
	rec := request(handler, http.MethodGet, "/_/api/connectors/portfolio", "secret", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), sum) {
		t.Fatalf("current = %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodGet, "/_/api/connectors", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("list without token = %d, want 401", rec.Code)
	}

	next, nextSum := certificatePEM(t)
	_, otherSum := certificatePEM(t)
	same := jsonBody(t, map[string]string{"expected_sha256": nextSum, "certificate_pem": next})
	if rec := local(handler, http.MethodPost, "/_/api/connectors/portfolio/rotate", same); rec.Code != http.StatusBadRequest {
		t.Fatalf("rotate to the expected certificate = %d, want 400", rec.Code)
	}
	stale := jsonBody(t, map[string]string{"expected_sha256": otherSum, "certificate_pem": next})
	if rec := local(handler, http.MethodPost, "/_/api/connectors/portfolio/rotate", stale); rec.Code != http.StatusConflict {
		t.Fatalf("rotate with a stale expectation = %d, want 409", rec.Code)
	}
	rotate := jsonBody(t, map[string]string{"expected_sha256": sum, "certificate_pem": next})
	if rec := local(handler, http.MethodPost, "/_/api/connectors/portfolio/rotate", rotate); rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d %s", rec.Code, rec.Body)
	}
	if rec := local(handler, http.MethodPost, "/_/api/connectors/portfolio/revoke", ""); rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodGet, "/_/api/connectors/portfolio", "secret", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("current after revoke = %d, want 404", rec.Code)
	}

	var kinds []events.Type
	for _, e := range recorder.Events() {
		if e.Actor != "local" || e.Host != "portfolio" {
			t.Fatalf("event %+v, want actor local on portfolio", e)
		}
		kinds = append(kinds, e.Type)
	}
	want := []events.Type{events.ConnectorEnrolled, events.ConnectorRotated, events.ConnectorRevoked}
	if len(kinds) != len(want) || kinds[0] != want[0] || kinds[1] != want[1] || kinds[2] != want[2] {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
}

func TestConnectorEnrolRefusesPrivateKey(t *testing.T) {
	handler, _ := connectorHandler(t)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	body := jsonBody(t, map[string]string{"name": "portfolio", "certificate_pem": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	if rec := local(handler, http.MethodPost, "/_/api/connectors", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("private key enrolled: %d %s", rec.Code, rec.Body)
	}
}
