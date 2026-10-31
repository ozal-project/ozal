package updateexec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/updatejob"
	"github.com/ozal-project/ozal/internal/updatelock"
)

type fakeRunner struct {
	mu      sync.Mutex
	calls   [][]string
	plan    updatejob.Plan
	fail    error
	block   chan struct{}
	started chan struct{}
	once    sync.Once
}

func (f *fakeRunner) Run(_ context.Context, args []string, _ io.Writer) error {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()
	if planPath := argValue(args, "-plan"); planPath != "" {
		if f.plan.ID == "" {
			id, err := updatejob.NewID()
			if err != nil {
				return err
			}
			f.plan.ID = id
		}
		if f.plan.CheckedAt == "" {
			f.plan.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		}
		if f.plan.Result == "" {
			f.plan.Result = "available"
		}
		if f.plan.Channel == "" {
			f.plan.Channel = "stable"
		}
		if f.plan.Platform == "" {
			f.plan.Platform = runtime.GOOS + "/" + runtime.GOARCH
		}
		if err := updatejob.WritePlan(planPath, f.plan); err != nil {
			return err
		}
		return f.fail
	}
	if f.block != nil {
		f.once.Do(func() { close(f.started) })
		<-f.block
	}
	id, dir := argValue(args, "-job"), argValue(args, "-jobs")
	if id == "" || dir == "" {
		return errors.New("fake runner got no job")
	}
	store := updatejob.Store{Dir: dir}
	if _, err := store.Read(id); err != nil {
		return err
	}
	if f.fail != nil {
		return f.fail
	}
	_, err := store.Advance(id, updatejob.PhaseComplete, updatejob.ResultUpdated, "test update")
	return err
}

func argValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func newTestServer(t *testing.T, runner Runner) *Server {
	t.Helper()
	dir := t.TempDir()
	return &Server{
		Profile: Profile{
			Socket: filepathJoin(dir, "executor.sock"), Unit: "ozal",
			Binary: filepathJoin(dir, "ozal"), Config: filepathJoin(dir, "config.json"),
			PublicKey: filepathJoin(dir, "release.pub"), CatalogBase: "https://ozal.dev/api/v1/releases",
			Channel: "stable", Status: filepathJoin(dir, "update-status.json"),
			State: filepathJoin(dir, "catalog-state.json"), Jobs: filepathJoin(dir, "jobs"),
		},
		Store: updatejob.Store{Dir: filepathJoin(dir, "jobs")}, Runner: runner,
	}
}

func filepathJoin(parts ...string) string { return strings.Join(parts, "/") }

func TestExecutorPlanReturnsTheReviewedCandidate(t *testing.T) {
	runner := &fakeRunner{plan: updatejob.Plan{
		Installed: "0.1.0", Latest: "v0.2.0", Channel: "stable", Result: "available",
		Reason: "v0.2.0 is newer", ArchiveSHA256: strings.Repeat("a", 64), Size: 123,
	}}
	s := newTestServer(t, runner)
	plan, err := s.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Latest != "v0.2.0" || plan.Installed != "0.1.0" || plan.Result != "available" || plan.ID == "" {
		t.Fatalf("plan %+v", plan)
	}
	if !strings.Contains(strings.Join(runner.calls[0], " "), "-catalog https://ozal.dev/api/v1/releases/stable/"+runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("catalog args %v", runner.calls[0])
	}
	for _, arg := range runner.calls[0] {
		if arg == "-apply" {
			t.Fatal("the plan check asked to install")
		}
	}
}

func TestExecutorRefusesMissingStaleAndUnchangedPlans(t *testing.T) {
	runner := &fakeRunner{plan: updatejob.Plan{
		Installed: "0.1.0", Latest: "v0.2.0", Channel: "stable", Result: "available",
		ArchiveSHA256: strings.Repeat("a", 64),
	}}
	s := newTestServer(t, runner)
	if _, err := s.Start(context.Background(), strings.Repeat("b", 32)); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("no review: %v", err)
	}
	plan, err := s.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(context.Background(), strings.Repeat("b", 32)); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("wrong plan: %v", err)
	}

	old := plan
	old.CheckedAt = time.Now().Add(-updatejob.ReviewTTL - time.Minute).UTC().Format(time.RFC3339)
	s.plan = &old
	if _, err := s.Start(context.Background(), old.ID); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("stale plan: %v", err)
	}

	current := plan
	current.Result = "current"
	current.Latest = ""
	s.plan = &current
	if _, err := s.Start(context.Background(), current.ID); !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("current plan: %v", err)
	}
}

func TestExecutorRunsOneJobAndReturnsTheSameJobOnRepeat(t *testing.T) {
	runner := &fakeRunner{
		plan: updatejob.Plan{Installed: "0.1.0", Latest: "v0.2.0", Channel: "stable", Result: "available",
			ArchiveSHA256: strings.Repeat("a", 64)},
		block: make(chan struct{}), started: make(chan struct{}),
	}
	s := newTestServer(t, runner)
	plan, err := s.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Start(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the fake update did not start")
	}
	second, err := s.Start(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("double start created %s and %s", first.ID, second.ID)
	}
	close(runner.block)
	waitTerminal(t, s.Store, first.ID)
	job, err := s.Store.Read(first.ID)
	if err != nil || job.Result != updatejob.ResultUpdated {
		t.Fatalf("job %+v err %v", job, err)
	}
	args := strings.Join(runner.calls[len(runner.calls)-1], " ")
	if !strings.Contains(args, "-expect v0.2.0") || !strings.Contains(args, "-expect-sha256 "+plan.ArchiveSHA256) || !strings.Contains(args, "-apply") {
		t.Fatalf("apply args %q", args)
	}
}

func waitTerminal(t *testing.T, store updatejob.Store, id string) updatejob.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := store.Read(id)
		if err == nil && job.Terminal() {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the job did not finish")
	return updatejob.Job{}
}

func TestExecutorRecoveryMarksInterruptedWorkForAPerson(t *testing.T) {
	s := newTestServer(t, &fakeRunner{})
	id, err := updatejob.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Create(updatejob.Job{ID: id, Channel: "stable", From: "0.1.0", To: "v0.2.0",
		Phase: updatejob.PhaseInstalling, Result: updatejob.ResultPending}); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	job, err := s.Store.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	if job.Result != updatejob.ResultInterventionRequired || !strings.Contains(job.Reason, "restarted") {
		t.Fatalf("job %+v", job)
	}
}

func TestExecutorStartAPIRequiresConfirmationAndExactlyOneDocument(t *testing.T) {
	runner := &fakeRunner{plan: updatejob.Plan{Installed: "0.1.0", Latest: "v0.2.0", Channel: "stable",
		Result: "available", ArchiveSHA256: strings.Repeat("a", 64)}}
	s := newTestServer(t, runner)
	plan, err := s.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	for _, body := range []string{
		`{"plan_id":"` + plan.ID + `"}`,
		`{"plan_id":"` + plan.ID + `","confirm":true,"command":"reboot"}`,
		`{"plan_id":"bad","confirm":true}`,
		`{"plan_id":"` + plan.ID + `","confirm":true}{}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s = %d, want 400", body, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(`{"plan_id":"`+plan.ID+`","confirm":true}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
}

func TestExecutorJobAPIOnlyReturnsValidatedRecords(t *testing.T) {
	s := newTestServer(t, &fakeRunner{})
	handler := s.Handler()
	for _, path := range []string{"/v1/jobs/not-an-id", "/v1/jobs/" + strings.Repeat("a", 32)} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
	if rec := doGet(handler, "/v1/jobs/latest"); rec.Code != http.StatusNotFound {
		t.Fatalf("latest = %d", rec.Code)
	}
}

func doGet(handler http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestExecutorPlanRejectsAPlanForAnotherChannelOrPlatform(t *testing.T) {
	for _, mutate := range []func(*updatejob.Plan){
		func(p *updatejob.Plan) { p.Channel = "preview" },
		func(p *updatejob.Plan) { p.Platform = "plan9/riscv64" },
	} {
		runner := &fakeRunner{plan: updatejob.Plan{Installed: "0.1.0", Latest: "v0.2.0", Channel: "stable",
			Platform: runtime.GOOS + "/" + runtime.GOARCH, Result: "available", ArchiveSHA256: strings.Repeat("a", 64)}}
		mutate(&runner.plan)
		s := newTestServer(t, runner)
		if _, err := s.Plan(context.Background()); err == nil {
			t.Fatalf("plan %+v accepted", runner.plan)
		}
	}
}

func TestExecutorPlanWithNoFileIsRefused(t *testing.T) {
	s := newTestServer(t, failingRunner{})
	if _, err := s.Plan(context.Background()); err == nil {
		t.Fatal("offline check produced a trusted plan")
	}
}

type failingRunner struct{}

func (failingRunner) Run(context.Context, []string, io.Writer) error {
	return errors.New("offline")
}

func TestExecutorResponsesNeverCache(t *testing.T) {
	s := newTestServer(t, &fakeRunner{plan: updatejob.Plan{Installed: "0.1.0", Latest: "v0.2.0", Channel: "stable",
		Result: "available", ArchiveSHA256: strings.Repeat("a", 64)}})
	rec := doGet(s.Handler(), "/v1/healthz")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("health = %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	raw, _ := json.Marshal(map[string]bool{"ok": true})
	if strings.TrimSpace(rec.Body.String()) != string(raw) {
		t.Fatalf("health body %q", rec.Body.String())
	}
}

func TestExecutorRefusesToReplaceALiveSocket(t *testing.T) {
	profile, _ := validProfileFile(t)
	listener, err := net.Listen("unix", profile.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &Server{Profile: profile, Store: updatejob.Store{Dir: profile.Jobs}, Runner: &fakeRunner{}}
	err = server.Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already listening") {
		t.Fatalf("err = %v, want a live-socket refusal", err)
	}
}

func TestExecutorRecoveryLeavesAChildThatStillHoldsTheLockAlone(t *testing.T) {
	s := newTestServer(t, &fakeRunner{})
	id, err := updatejob.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Create(updatejob.Job{ID: id, Channel: "stable", From: "0.1.0", To: "v0.2.0",
		Phase: updatejob.PhaseInstalling, Result: updatejob.ResultPending}); err != nil {
		t.Fatal(err)
	}
	lock, err := updatelock.Acquire(filepath.Join(filepath.Dir(s.Profile.Binary), "update.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	job, err := s.Store.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	if job.Terminal() {
		t.Fatalf("recovery overwrote a live update: %+v", job)
	}
}

func TestExecutorPlanRefusesAnInstallablePlanFromAFailedCheck(t *testing.T) {
	runner := &fakeRunner{
		plan: updatejob.Plan{Installed: "0.1.0", Latest: "v0.2.0", Channel: "stable",
			Result: "available", ArchiveSHA256: strings.Repeat("a", 64)},
		fail: errors.New("the observed sequence could not be persisted"),
	}
	s := newTestServer(t, runner)
	if _, err := s.Plan(context.Background()); err == nil {
		t.Fatal("a failed check produced an installable plan")
	}
}
