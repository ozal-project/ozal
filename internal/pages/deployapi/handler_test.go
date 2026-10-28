package deployapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/pages"
	"github.com/switchboard-project/switchboard/internal/pages/deployauth"
	"github.com/switchboard-project/switchboard/internal/pipeline"
	"github.com/switchboard-project/switchboard/internal/ratelimit"
)

type verifyFunc func(context.Context, string) (deployauth.Claims, error)

func (f verifyFunc) Verify(ctx context.Context, v string) (deployauth.Claims, error) {
	return f(ctx, v)
}

type replayMemory struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (s *replayMemory) Consume(_ context.Context, jti string, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[jti] {
		return false, nil
	}
	s.seen[jti] = true
	return true, nil
}

type testStore struct {
	calls            atomic.Int32
	entered, release chan struct{}
	project          string
	fail             error
}

func (s *testStore) DeployProject(_ context.Context, site string, source pages.SourceMetadata, body io.Reader) (pages.Deployment, error) {
	s.calls.Add(1)
	if s.entered != nil {
		close(s.entered)
		<-s.release
	}
	if _, err := io.ReadAll(body); err != nil {
		return pages.Deployment{}, err
	}
	if s.fail != nil {
		return pages.Deployment{}, s.fail
	}
	return pages.Deployment{Site: site, Project: source.Project, ID: strings.Repeat("a", 64), CommitSHA: source.CommitSHA, RunURL: source.RunURL, Live: true}, nil
}
func (s *testStore) Deployment(_ context.Context, site, id string) (pages.Deployment, error) {
	return pages.Deployment{Site: site, ID: id, Project: s.project, Live: true}, nil
}

type probeBody struct{ read atomic.Bool }

func (b *probeBody) Read([]byte) (int, error) { b.read.Store(true); return 0, io.EOF }
func (*probeBody) Close() error               { return nil }

type brokenAudit struct{}

type brokenReplays struct{}

func (brokenReplays) Consume(context.Context, string, time.Time) (bool, error) {
	return false, errors.New("private replay database path")
}

func (brokenAudit) Append(context.Context, events.Event) error {
	return errors.New("private disk error /secret/path")
}

func deployFixture(t *testing.T) (Options, *testStore, *events.Recorder) {
	t.Helper()
	t.Setenv("TEST_PROJECT_DEPLOY", "project-secret")
	t.Setenv("TEST_OTHER_DEPLOY", "other-secret")
	store, sink := &testStore{project: "portfolio"}, &events.Recorder{}
	projects := []config.Project{
		{Name: "portfolio", Site: "portfolio", Deployers: []config.ProjectDeployer{{Token: "env:TEST_PROJECT_DEPLOY"}}},
		{Name: "other", Site: "other", Deployers: []config.ProjectDeployer{{Token: "env:TEST_OTHER_DEPLOY"}}},
	}
	return Options{Host: "deploy.example", Audience: "https://deploy.example", Projects: func() []config.Project { return projects }, Store: store, Sink: sink, Replays: &replayMemory{seen: map[string]bool{}}}, store, sink
}
func callDeploy(h http.Handler, method, path, token string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://deploy.example"+path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const uploadPath = "/v1/projects/portfolio/deployments"

// testWorkflowRef is the deploy workflow every GitHub fixture trusts.
const testWorkflowRef = "owner/repo/.github/workflows/deploy.yml@refs/heads/main"

func TestDuplicateAuthorizationAndSpoofedClientIPDoNotBypassChecks(t *testing.T) {
	opts, store, _ := deployFixture(t)
	h, _ := New(opts)
	probe := &probeBody{}
	req := httptest.NewRequest("POST", "http://deploy.example"+uploadPath, probe)
	req.Header.Add("Authorization", "Bearer project-secret")
	req.Header.Add("Authorization", "Bearer project-secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != 401 || probe.read.Load() || store.calls.Load() != 0 {
		t.Fatalf("duplicate authorization=%d,read=%v", response.Code, probe.read.Load())
	}
	now := time.Now()
	opts.IPLimiter = ratelimit.NewLimiter(1, 1, time.Hour, ratelimit.WithNow(func() time.Time { return now }))
	h, _ = New(opts)
	trusted := pipeline.Trust(h)
	for i, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		probe = &probeBody{}
		req = httptest.NewRequest("POST", "http://deploy.example"+uploadPath, probe)
		req.RemoteAddr = "203.0.113.7:1234"
		req.Header.Set("CF-Connecting-IP", ip)
		req.Header.Set("Authorization", "Bearer project-secret")
		response = httptest.NewRecorder()
		trusted.ServeHTTP(response, req)
		if i == 0 && response.Code != 201 {
			t.Fatalf("first request=%d", response.Code)
		}
		if i == 1 && (response.Code != 429 || probe.read.Load()) {
			t.Fatalf("spoofed forwarding header bypassed IP budget: %d,read=%v", response.Code, probe.read.Load())
		}
	}
}

func TestProjectTokenScopeAndAuthenticationPrecedeBody(t *testing.T) {
	opts, store, sink := deployFixture(t)
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "wrong", "other-secret"} {
		body := &probeBody{}
		res := callDeploy(h, "POST", uploadPath, token, body)
		if res.Code != 401 || body.read.Load() || store.calls.Load() != 0 {
			t.Fatalf("unauthenticated request status=%d,read=%v,calls=%d", res.Code, body.read.Load(), store.calls.Load())
		}
	}
	res := callDeploy(h, "POST", uploadPath, "project-secret", strings.NewReader("archive"))
	if res.Code != 201 || store.calls.Load() != 1 {
		t.Fatalf("authenticated upload=%d %s", res.Code, res.Body)
	}
	if !sink.Has(events.PagesDeployed) || !sink.Has(events.PagesDeployRefused) {
		t.Fatal("missing reviewed deploy outcome events")
	}
	for _, event := range sink.Events() {
		if event.Error != "" || strings.Contains(event.Reason, "secret") {
			t.Fatalf("raw secret/error in event: %+v", event)
		}
	}
}

func TestProjectTokenFileRotationAndUnreadableSecretFailClosed(t *testing.T) {
	opts, store, _ := deployFixture(t)
	file := filepath.Join(t.TempDir(), "deploy.token")
	if err := os.WriteFile(file, []byte("first-file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.Projects = func() []config.Project {
		return []config.Project{{Name: "portfolio", Site: "portfolio", Deployers: []config.ProjectDeployer{{Token: "file:" + file}}}}
	}
	h, _ := New(opts)
	if response := callDeploy(h, "POST", uploadPath, "first-file-secret", strings.NewReader("a")); response.Code != 201 {
		t.Fatalf("file secret=%d %s", response.Code, response.Body)
	}
	if err := os.WriteFile(file, []byte("rotated-file-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := &probeBody{}
	if response := callDeploy(h, "POST", uploadPath, "first-file-secret", probe); response.Code != 401 || probe.read.Load() {
		t.Fatalf("revoked file token=%d,read=%v", response.Code, probe.read.Load())
	}
	if response := callDeploy(h, "POST", uploadPath, "rotated-file-secret", strings.NewReader("a")); response.Code != 201 {
		t.Fatalf("rotated token=%d", response.Code)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	probe = &probeBody{}
	response := callDeploy(h, "POST", uploadPath, "rotated-file-secret", probe)
	if response.Code != 503 || probe.read.Load() || store.calls.Load() != 2 || strings.Contains(response.Body.String(), file) {
		t.Fatalf("unreadable file=%d %s,read=%v", response.Code, response.Body, probe.read.Load())
	}
}

func TestDeniedUploadRespondsOnTheWireBeforeTheBody(t *testing.T) {
	opts, _, _ := deployFixture(t)
	h, _ := New(opts)
	server := httptest.NewServer(RouteHost("deploy.example", h, http.NotFoundHandler()))
	defer server.Close()
	for _, tc := range []struct {
		method, path, host string
		status             int
	}{{"POST", uploadPath, "deploy.example", 401}, {"POST", "/_/reload", "deploy.example", 404}, {"HEAD", uploadPath, "deploy.example", 405}, {"POST", "/v1//projects/portfolio/deployments", "deploy.example", 404}, {"POST", uploadPath, "deploy.example:invalid", 400}} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			connection, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			// No body is sent. net/http must not drain even this small declared body.
			_, err = fmt.Fprintf(connection, "%s %s HTTP/1.1\r\nHost: %s\r\nContent-Length: 64\r\n\r\n", tc.method, tc.path, tc.host)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(connection), nil)
			if err != nil {
				t.Fatalf("denial waited for upload body: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("wire denial=%d", response.StatusCode)
			}
		})
	}
}

func validOIDCClaims() deployauth.Claims {
	now := time.Now()
	return deployauth.Claims{RepositoryID: 123, RepositoryOwnerID: 42, Ref: "refs/heads/main", WorkflowRef: testWorkflowRef, EventName: "push", Audience: []string{"https://deploy.example"}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(4 * time.Minute), ObservedAt: now, JWTID: "once", CommitSHA: strings.Repeat("a", 40), Repository: "owner/repo", RunID: "42"}
}

func TestOIDCTrustAndReplayRefusalsDoNotReadBody(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*deployauth.Claims)
	}{
		{"repository", "deployer.repository", func(c *deployauth.Claims) { c.RepositoryID++ }},
		{"owner", "deployer.owner", func(c *deployauth.Claims) { c.RepositoryOwnerID++ }},
		{"ref", "deployer.ref", func(c *deployauth.Claims) { c.Ref = "refs/heads/evil" }},
		{"audience", "deployer.audience", func(c *deployauth.Claims) { c.Audience = []string{"evil"} }},
		{"expired", "deployer.expired", func(c *deployauth.Claims) { c.ExpiresAt = c.ObservedAt.Add(-time.Second) }},
		{"other workflow", "deployer.workflow", func(c *deployauth.Claims) { c.WorkflowRef = "owner/repo/.github/workflows/triage.yml@refs/heads/main" }},
		{"pull_request_target on main", "deployer.event", func(c *deployauth.Claims) { c.EventName = "pull_request_target" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, store, _ := deployFixture(t)
			claims := validOIDCClaims()
			tc.change(&claims)
			opts.Projects = func() []config.Project {
				return []config.Project{{Name: "portfolio", Site: "portfolio", Deployers: []config.ProjectDeployer{{GitHub: &config.ProjectGitHubTrust{RepositoryID: 123, RepositoryOwnerID: 42, Ref: "refs/heads/main", WorkflowRef: testWorkflowRef}}}}}
			}
			opts.Verifier = verifyFunc(func(context.Context, string) (deployauth.Claims, error) { return claims, nil })
			h, _ := New(opts)
			body := &probeBody{}
			res := callDeploy(h, "POST", uploadPath, "signed-test-token", body)
			if res.Code != 401 || !strings.Contains(res.Body.String(), tc.code) || body.read.Load() || store.calls.Load() != 0 {
				t.Fatalf("refusal=%d %s,read=%v", res.Code, res.Body, body.read.Load())
			}
		})
	}
	opts, store, _ := deployFixture(t)
	claims := validOIDCClaims()
	opts.Projects = func() []config.Project {
		return []config.Project{{Name: "portfolio", Site: "portfolio", Deployers: []config.ProjectDeployer{{GitHub: &config.ProjectGitHubTrust{RepositoryID: 123, RepositoryOwnerID: 42, Ref: "refs/heads/main", WorkflowRef: testWorkflowRef}}}}}
	}
	opts.Verifier = verifyFunc(func(context.Context, string) (deployauth.Claims, error) { return claims, nil })
	h, _ := New(opts)
	if res := callDeploy(h, "POST", uploadPath, "same", strings.NewReader("archive")); res.Code != 201 {
		t.Fatalf("valid OIDC=%d %s", res.Code, res.Body)
	}
	body := &probeBody{}
	res := callDeploy(h, "POST", uploadPath, "same", body)
	if res.Code != 401 || !strings.Contains(res.Body.String(), "deployer.replay") || body.read.Load() || store.calls.Load() != 1 {
		t.Fatalf("replay=%d %s,read=%v", res.Code, res.Body, body.read.Load())
	}
}

func TestRequiredAuditAndProjectRateLimitPrecedeBody(t *testing.T) {
	for _, required := range []bool{true, false} {
		t.Run(map[bool]string{true: "required", false: "best_effort"}[required], func(t *testing.T) {
			opts, store, _ := deployFixture(t)
			opts.Audit = brokenAudit{}
			opts.AuditEnabled = true
			opts.AuditRequired = required
			h, _ := New(opts)
			body := &probeBody{}
			res := callDeploy(h, "POST", uploadPath, "project-secret", body)
			if required && (res.Code != 503 || body.read.Load() || store.calls.Load() != 0) {
				t.Fatalf("required audit=%d,read=%v", res.Code, body.read.Load())
			}
			if !required && (res.Code != 201 || store.calls.Load() != 1) {
				t.Fatalf("best effort=%d %s", res.Code, res.Body)
			}
			if strings.Contains(res.Body.String(), "/secret/path") {
				t.Fatal("raw audit error leaked")
			}
		})
	}
	opts, _, _ := deployFixture(t)
	now := time.Now()
	opts.ProjectLimiter = ratelimit.NewLimiter(1, 1, time.Hour, ratelimit.WithNow(func() time.Time { return now }))
	h, _ := New(opts)
	if res := callDeploy(h, "POST", uploadPath, "project-secret", strings.NewReader("a")); res.Code != 201 {
		t.Fatalf("first=%d", res.Code)
	}
	body := &probeBody{}
	res := callDeploy(h, "POST", uploadPath, "project-secret", body)
	if res.Code != 429 || body.read.Load() {
		t.Fatalf("rate limit=%d,read=%v", res.Code, body.read.Load())
	}
	if res := callDeploy(h, "POST", "/v1/projects/other/deployments", "other-secret", strings.NewReader("a")); res.Code != 201 {
		t.Fatalf("other project spent the first project's budget: %d", res.Code)
	}
}

func TestRequiredAuditCannotBeDisabledByAnInconsistentOption(t *testing.T) {
	opts, store, _ := deployFixture(t)
	opts.AuditRequired = true
	opts.AuditEnabled = false
	opts.Audit = nil
	h, err := New(opts)
	if err != nil {
		return
	} // Refusing construction also fails closed.
	probe := &probeBody{}
	response := callDeploy(h, "POST", uploadPath, "project-secret", probe)
	if response.Code != 503 || probe.read.Load() || store.calls.Load() != 0 {
		t.Fatalf("required audit was bypassed: %d,read=%v,calls=%d", response.Code, probe.read.Load(), store.calls.Load())
	}
}

func TestReplayStoreFailureCannotAuthorizeAnUpload(t *testing.T) {
	opts, store, _ := deployFixture(t)
	opts.Replays = brokenReplays{}
	claims := validOIDCClaims()
	opts.Verifier = verifyFunc(func(context.Context, string) (deployauth.Claims, error) { return claims, nil })
	opts.Projects = func() []config.Project {
		return []config.Project{{Name: "portfolio", Site: "portfolio", Deployers: []config.ProjectDeployer{{GitHub: &config.ProjectGitHubTrust{RepositoryID: 123, RepositoryOwnerID: 42, Ref: "refs/heads/main", WorkflowRef: testWorkflowRef}}}}}
	}
	h, _ := New(opts)
	probe := &probeBody{}
	response := callDeploy(h, "POST", uploadPath, "signed-test-token", probe)
	if response.Code != 503 || probe.read.Load() || store.calls.Load() != 0 || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("replay store failure=%d %s,read=%v", response.Code, response.Body, probe.read.Load())
	}
}

func TestStatusReadsDoNotSpendTheDeployTokenOrBudget(t *testing.T) {
	opts, store, _ := deployFixture(t)
	claims := validOIDCClaims()
	opts.Projects = func() []config.Project {
		return []config.Project{{Name: "portfolio", Site: "portfolio", Deployers: []config.ProjectDeployer{{GitHub: &config.ProjectGitHubTrust{RepositoryID: 123, RepositoryOwnerID: 42, Ref: "refs/heads/main", WorkflowRef: testWorkflowRef}}}}}
	}
	opts.Verifier = verifyFunc(func(context.Context, string) (deployauth.Claims, error) { return claims, nil })
	// Two deployments per hour. If the two status reads spent this budget, the
	// second upload would be rate limited instead of reaching the replay check.
	opts.ProjectLimiter = ratelimit.NewLimiter(1.0/3600, 2, time.Hour)
	h, _ := New(opts)
	if res := callDeploy(h, "POST", uploadPath, "same", strings.NewReader("archive")); res.Code != 201 {
		t.Fatalf("upload=%d %s", res.Code, res.Body)
	}
	statusPath := uploadPath + "/" + strings.Repeat("a", 64)
	for i := range 2 {
		if res := callDeploy(h, "GET", statusPath, "same", nil); res.Code != 200 {
			t.Fatalf("status read %d with the upload token=%d %s", i+1, res.Code, res.Body)
		}
	}
	// Reading never re-arms the token for writing.
	body := &probeBody{}
	if res := callDeploy(h, "POST", uploadPath, "same", body); res.Code != 401 || !strings.Contains(res.Body.String(), "deployer.replay") || body.read.Load() {
		t.Fatalf("second upload with a spent token=%d %s", res.Code, res.Body)
	}
	if store.calls.Load() != 1 {
		t.Fatalf("store calls=%d, want 1", store.calls.Load())
	}
}

func TestUnauthenticatedNoiseDoesNotFloodActivity(t *testing.T) {
	opts, _, sink := deployFixture(t)
	h, _ := New(opts)
	noise := []struct{ method, path string }{
		{"GET", "/"},
		{"GET", "/.env"},
		{"DELETE", uploadPath},
		{"POST", "/v1/projects/unknown/deployments"},
		{"POST", "/v1/projects/../admin"},
	}
	for _, request := range noise {
		if res := callDeploy(h, request.method, request.path, "guess", strings.NewReader("x")); res.Code < 400 {
			t.Fatalf("%s %s = %d, want a refusal", request.method, request.path, res.Code)
		}
	}
	if got := len(sink.Events()); got != 0 {
		t.Fatalf("scanner requests emitted %d activity events: %+v", got, sink.Events())
	}
	// A refusal for a configured project is still visible to the operator.
	if res := callDeploy(h, "POST", uploadPath, "wrong-token", &probeBody{}); res.Code != 401 {
		t.Fatalf("wrong token=%d", res.Code)
	}
	if got := sink.Events(); len(got) != 1 || got[0].Type != events.PagesDeployRefused || got[0].Code != "deployer.token_invalid" {
		t.Fatalf("project refusal events=%+v", got)
	}
}

func TestOneUploadPerProjectAndStatusScope(t *testing.T) {
	opts, store, _ := deployFixture(t)
	store.entered = make(chan struct{})
	store.release = make(chan struct{})
	h, _ := New(opts)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- callDeploy(h, "POST", uploadPath, "project-secret", strings.NewReader("a")) }()
	select {
	case <-store.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first upload did not reach store")
	}
	body := &probeBody{}
	res := callDeploy(h, "POST", uploadPath, "project-secret", body)
	close(store.release)
	if res.Code != 409 || body.read.Load() {
		t.Fatalf("concurrent upload=%d,read=%v", res.Code, body.read.Load())
	}
	if res := <-done; res.Code != 201 {
		t.Fatalf("first upload=%d", res.Code)
	}
	path := uploadPath + "/" + strings.Repeat("a", 64)
	if res := callDeploy(h, "GET", path, "project-secret", nil); res.Code != 200 {
		t.Fatalf("project status=%d", res.Code)
	}
	store.project = "other"
	if res := callDeploy(h, "GET", path, "project-secret", nil); res.Code != 404 {
		t.Fatalf("cross-project status=%d", res.Code)
	}
}

func TestOversizedUploadAndRawStorageErrorsAreSafe(t *testing.T) {
	opts, store, sink := deployFixture(t)
	h, _ := New(opts)
	body := &probeBody{}
	req := httptest.NewRequest("POST", "http://deploy.example"+uploadPath, body)
	req.ContentLength = pages.MaxUploadBytes + 1
	req.Header.Set("Authorization", "Bearer project-secret")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 413 || body.read.Load() || store.calls.Load() != 0 {
		t.Fatalf("oversized=%d,read=%v", res.Code, body.read.Load())
	}
	store.fail = errors.New("write /secret/path containing project-secret")
	res = callDeploy(h, "POST", uploadPath, "project-secret", strings.NewReader("a"))
	if res.Code != 503 || strings.Contains(res.Body.String(), "secret") {
		t.Fatalf("storage failure=%d %s", res.Code, res.Body)
	}
	for _, event := range sink.Events() {
		if event.Error != "" || strings.Contains(event.Reason, "/secret/path") {
			t.Fatalf("raw storage error=%+v", event)
		}
	}
}
