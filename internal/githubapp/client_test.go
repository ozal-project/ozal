package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/switchboard-project/switchboard/internal/config"
)

func TestDiscoveryUsesOnlyMetadataAndNeverReturnsCredentials(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_GITHUB_APP_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})))
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/app/installations/42/access_tokens":
			token, err := jwt.ParseSigned(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), []jose.SignatureAlgorithm{jose.RS256})
			if err != nil {
				t.Error(err)
				w.WriteHeader(403)
				return
			}
			var claims jwt.Claims
			if err := token.Claims(&key.PublicKey, &claims); err != nil {
				t.Error(err)
			}
			if claims.Issuer != "7" || claims.Expiry.Time().Sub(claims.IssuedAt.Time()) > 10*time.Minute {
				t.Error("invalid app authority/lifetime")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			raw, _ := json.Marshal(body)
			if string(raw) != `{"permissions":{"metadata":"read"}}` {
				t.Error("requested extra permissions")
			}
			w.Write([]byte(`{"token":"installation-secret"}`))
		case "/installation/repositories":
			if r.Header.Get("Authorization") != "Bearer installation-secret" || r.URL.Query().Get("page") != "2" {
				t.Error("wrong installation or page")
			}
			w.Write([]byte(`{"total_count":101,"repositories":[{"id":123,"full_name":"acme/site","owner":{"id":8},"default_branch":"main","private":true}]}`))
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	result, err := client.Repositories(context.Background(), config.GitHubApp{AppID: 7, InstallationID: 42, PrivateKey: "env:TEST_GITHUB_APP_KEY"}, 2)
	if err != nil || calls != 2 || len(result.Repositories) != 1 || result.More {
		t.Fatalf("discovery: %+v %v", result, err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "installation-secret") || strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatal("secret in metadata")
	}
}

func TestDiscoveryErrorsAreRedactedAndKeyReferenceRequired(t *testing.T) {
	client := New()
	_, err := client.Repositories(context.Background(), config.GitHubApp{PrivateKey: "env:ABSENT_TEST_GITHUB_KEY"}, 1)
	if err != ErrUnavailable {
		t.Fatal("raw secret resolution error")
	}
	if _, err := client.Repositories(context.Background(), config.GitHubApp{}, 0); err != ErrUnavailable {
		t.Fatal("invalid page accepted")
	}
}

func TestWriteTokenIsRestrictedToSelectedRepository(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_GITHUB_APP_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RepositoryIDs []int64           `json:"repository_ids"`
			Permissions   map[string]string `json:"permissions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.RepositoryIDs) != 1 || body.RepositoryIDs[0] != 123 || len(body.Permissions) != 4 || body.Permissions["metadata"] != "read" || body.Permissions["contents"] != "write" || body.Permissions["workflows"] != "write" || body.Permissions["pull_requests"] != "write" {
			t.Errorf("incorrect token scope: %+v", body)
		}
		w.Write([]byte(`{"token":"test-installation-token"}`))
	}))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	if _, err := client.installationToken(context.Background(), config.GitHubApp{AppID: 7, InstallationID: 42, PrivateKey: "env:TEST_GITHUB_APP_KEY"}, 123); err != nil {
		t.Fatal(err)
	}
}
