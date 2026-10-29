package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/ozal-project/ozal/internal/ctl/artifact"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testIssuer struct {
	calls int
	token string
}

func (s *testIssuer) Token(context.Context, string) (string, error) { s.calls++; return s.token, nil }
func ciSnapshot(t *testing.T) artifact.Snapshot {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("home"), 0600)
	os.WriteFile(filepath.Join(dir, "app.css"), []byte("body{}"), 0600)
	snap, err := artifact.FromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}
func TestCIValidatesBeforeCredentialsAndReservesReceipt(t *testing.T) {
	issuer := &testIssuer{token: "private-token"}
	snap := ciSnapshot(t)
	r := CIRequest{Endpoint: "https://deploy.example.com", Project: "test", Archive: snap.Archive}
	if _, err := CI(context.Background(), io.Discard, issuer, r); err != nil {
		t.Fatal(err)
	}
	if issuer.calls != 0 {
		t.Fatal("plan minted token")
	}
	r.Apply = true
	r.ResultFile = filepath.Join(t.TempDir(), "result")
	r.Archive = []byte("invalid")
	if _, err := CI(context.Background(), io.Discard, issuer, r); err == nil {
		t.Fatal("accepted invalid artifact")
	}
	if issuer.calls != 0 {
		t.Fatal("minted token before validation")
	}
	r.Archive = snap.Archive
	os.WriteFile(r.ResultFile, []byte("preserve"), 0600)
	if _, err := CI(context.Background(), io.Discard, issuer, r); err == nil {
		t.Fatal("overwrote result")
	}
	if issuer.calls != 0 {
		t.Fatal("minted token before reserving output")
	}
}
func TestCIConfirmsLiveBeforeStatusFailureAndNeverLeaksCredentials(t *testing.T) {
	for _, statusFail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "status failure"}[statusFail], func(t *testing.T) {
			token := "jwt-private-value"
			posts, gets := 0, 0
			receipt := Receipt{Project: "test", ID: strings.Repeat("a", 64), Live: true}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("missing auth")
				}
				if r.Method == "POST" {
					posts++
					if _, err := artifact.FromArchive(mustRead(t, r.Body)); err != nil {
						t.Error(err)
					}
					w.WriteHeader(201)
				} else {
					gets++
					if statusFail {
						w.WriteHeader(503)
						io.WriteString(w, token)
						return
					}
				}
				json.NewEncoder(w).Encode(receipt)
			}))
			defer server.Close()
			issuer := &testIssuer{token: token}
			var out bytes.Buffer
			confirmed := false
			path := filepath.Join(t.TempDir(), "receipt")
			got, err := CI(context.Background(), &out, issuer, CIRequest{Endpoint: server.URL, Project: "test", Archive: ciSnapshot(t).Archive, ResultFile: path, Apply: true, Client: server.Client(), Confirmed: func(Receipt) error { confirmed = true; return nil }})
			if (err != nil) != statusFail || !confirmed || posts != 1 || gets != 1 || got.ID != receipt.ID {
				t.Fatalf("%+v %v confirmed=%t calls=%d/%d", got, err, confirmed, posts, gets)
			}
			data, _ := os.ReadFile(path)
			combined := out.String() + string(data)
			if err != nil {
				combined += err.Error()
			}
			if strings.Contains(combined, token) {
				t.Fatal("credential leaked")
			}
		})
	}
}
func mustRead(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestOIDCTokenAudienceAndRedirectRefusal(t *testing.T) {
	leaked := false
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	redirect := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runner-secret" || r.URL.Query().Get("audience") != "https://deploy.example.com" {
			t.Error("wrong audience or auth")
		}
		if redirect {
			http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
			return
		}
		io.WriteString(w, `{"value":"private-jwt"}`)
	}))
	defer server.Close()
	issuer := GitHubTokenSource{URL: server.URL + "?existing=1", Authorization: "runner-secret", Client: server.Client()}
	if token, err := issuer.Token(context.Background(), "https://deploy.example.com"); err != nil || token != "private-jwt" {
		t.Fatal(token, err)
	}
	redirect = true
	if _, err := issuer.Token(context.Background(), "https://deploy.example.com"); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("followed redirect")
	}
}
func TestPublicAcceptanceUsesAssetAndRejectsFallback(t *testing.T) {
	snap := ciSnapshot(t)
	fallback := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			io.WriteString(w, "rewritten HTML")
		case "/app.css":
			io.WriteString(w, "body{}")
		default:
			if fallback {
				io.WriteString(w, "SPA")
			} else {
				http.NotFound(w, r)
			}
		}
	}))
	defer server.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+"/", http.StatusPermanentRedirect)
	}))
	defer plain.Close()
	req := Acceptance{URL: server.URL, HTTPURL: plain.URL, Proof: snap.Proof, Client: server.Client(), Attempts: 1}
	if err := Accept(context.Background(), io.Discard, req); err != nil {
		t.Fatal(err)
	}
	fallback = true
	if err := Accept(context.Background(), io.Discard, req); err == nil {
		t.Fatal("accepted SPA fallback")
	}
}
