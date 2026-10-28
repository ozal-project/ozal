package deployapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/pages"
	"github.com/switchboard-project/switchboard/internal/store/sqlite"
)

type orderedArchive struct {
	reader io.Reader
	read   bool
}

func (b *orderedArchive) Read(p []byte) (int, error) { b.read = true; return b.reader.Read(p) }

type orderedAudit struct {
	body    *orderedArchive
	records []events.Event
	t       *testing.T
}

// Append checks that the intent is durable before the archive is read and the
// outcome only after, and keeps both for the journal sequence assertion.
func (a *orderedAudit) Append(_ context.Context, e events.Event) error {
	a.records = append(a.records, e)
	intent := e.Code == "project.deploy"
	if intent && a.body.read {
		a.t.Error("durable intent came after body read")
	}
	if !intent && !a.body.read {
		a.t.Error("durable outcome came before the archive was processed")
	}
	if e.Deployment == nil || e.Deployment.RepositoryID != 123 {
		a.t.Errorf("unsafe/missing deployment provenance: %+v", e)
	}
	return nil
}

func projectArchive(t *testing.T, name, content string) *bytes.Reader {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tarWriter, content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(output.Bytes())
}

func TestSignedOIDCDeployUsesRealStoreReplayAndArchiveChecks(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	store, err := pages.NewStore(ctx, db, t.TempDir(), pages.DefaultKeep, pages.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: key.Public(), KeyID: "integration", Algorithm: "RS256", Use: "sig"}}})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != GitHubJWKS || r.Header.Get("Authorization") != "" {
			t.Fatalf("JWT sent outside verification boundary: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(jwks))}, nil
	})}
	issue := func(jti string) string {
		now := time.Now()
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "integration"))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := jwt.Signed(signer).Claims(jwt.Claims{Issuer: GitHubIssuer, Subject: "repo:owner/repo:ref:refs/heads/main", Audience: jwt.Audience{"https://deploy.example"}, IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), Expiry: jwt.NewNumericDate(now.Add(4 * time.Minute)), ID: jti}).Claims(map[string]any{
			"repository_id": "123", "repository_owner_id": "42", "ref": "refs/heads/main", "workflow_ref": testWorkflowRef, "event_name": "push", "sha": strings.Repeat("a", 40), "repository": "owner/repo", "run_id": "42",
		}).Serialize()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	body := &orderedArchive{reader: projectArchive(t, "index.html", "verified OIDC deployment")}
	audit := &orderedAudit{body: body, t: t}
	sink := &events.Recorder{}
	opts := Options{Host: "deploy.example", Audience: "https://deploy.example", Projects: func() []config.Project {
		return []config.Project{{Name: "portfolio", Site: "portfolio", Deployers: []config.ProjectDeployer{{GitHub: &config.ProjectGitHubTrust{RepositoryID: 123, RepositoryOwnerID: 42, Ref: "refs/heads/main", WorkflowRef: testWorkflowRef}}}}}
	}, Store: store, Verifier: NewGitHubVerifier(ctx, "https://deploy.example", client), Replays: sqlite.NewDeployReplays(db), Sink: sink, AuditEnabled: true, AuditRequired: true, Audit: audit}
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	token := issue("first")
	response := callDeploy(h, "POST", uploadPath, token, body)
	if response.Code != 201 {
		t.Fatalf("signed deployment=%d %s", response.Code, response.Body)
	}
	var deployed deploymentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &deployed); err != nil {
		t.Fatal(err)
	}
	if deployed.Project != "portfolio" || deployed.CommitSHA != strings.Repeat("a", 40) || deployed.RunURL != "https://github.com/owner/repo/actions/runs/42" || !deployed.Live {
		t.Fatalf("deployment provenance=%+v", deployed)
	}
	live, ok := store.Live("portfolio")
	if !ok {
		t.Fatal("deployment did not activate")
	}
	content, err := os.ReadFile(filepath.Join(live.Dir, "index.html"))
	if err != nil || string(content) != "verified OIDC deployment" {
		t.Fatalf("live site=%s,%v", content, err)
	}
	if response := callDeploy(h, "GET", uploadPath+"/"+deployed.ID, issue("read"), nil); response.Code != 200 || !strings.Contains(response.Body.String(), deployed.CommitSHA) {
		t.Fatalf("scoped status=%d %s", response.Code, response.Body)
	}
	probe := &probeBody{}
	if response := callDeploy(h, "POST", uploadPath, token, probe); response.Code != 401 || probe.read.Load() {
		t.Fatalf("replay=%d,read=%v", response.Code, probe.read.Load())
	}
	body = &orderedArchive{reader: projectArchive(t, "../escape", "malicious")}
	audit.body = body
	if response := callDeploy(h, "POST", uploadPath, issue("hostile"), body); response.Code != 422 || !strings.Contains(response.Body.String(), string(pages.UnsafePath)) {
		t.Fatalf("archive refusal=%d %s", response.Code, response.Body)
	}
	if after, _ := store.Live("portfolio"); after.Dir != live.Dir {
		t.Fatal("hostile archive changed the live deployment")
	}
	// A new handler and replay adapter over the same database still refuse it.
	opts.Replays = sqlite.NewDeployReplays(db)
	restarted, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	probe = &probeBody{}
	if response := callDeploy(restarted, "POST", uploadPath, token, probe); response.Code != 401 || probe.read.Load() {
		t.Fatalf("restart replay=%d,read=%v", response.Code, probe.read.Load())
	}
	// Only authenticated uploads reach the journal, each as intent + outcome.
	var journal []string
	for _, record := range audit.records {
		journal = append(journal, string(record.Type)+"/"+record.Code)
	}
	want := []string{
		string(events.AdminMutationRequested) + "/project.deploy", string(events.PagesDeployed) + "/deployment.created",
		string(events.AdminMutationRequested) + "/project.deploy", string(events.PagesDeployRefused) + "/" + string(pages.UnsafePath),
	}
	if !slices.Equal(journal, want) {
		t.Fatalf("durable journal=%v, want %v", journal, want)
	}
	for _, event := range sink.Events() {
		encoded, _ := json.Marshal(event)
		if strings.Contains(string(encoded), token) || event.Error != "" {
			t.Fatalf("event leaked credentials/error: %s", encoded)
		}
	}
}
