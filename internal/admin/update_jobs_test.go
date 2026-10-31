package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/updateexec"
	"github.com/ozal-project/ozal/internal/updatejob"
)

type fakeUpdateExecutor struct {
	plan    updatejob.Plan
	job     updatejob.Job
	err     error
	started string
}

func (f *fakeUpdateExecutor) Plan(context.Context) (updatejob.Plan, error) {
	if f.err != nil {
		return updatejob.Plan{}, f.err
	}
	return f.plan, nil
}

func (f *fakeUpdateExecutor) Start(_ context.Context, id string) (updatejob.Job, error) {
	f.started = id
	if f.err != nil {
		return updatejob.Job{}, f.err
	}
	return f.job, nil
}

func (f *fakeUpdateExecutor) Job(context.Context, string) (updatejob.Job, error) {
	if f.err != nil {
		return updatejob.Job{}, f.err
	}
	return f.job, nil
}

func (f *fakeUpdateExecutor) Latest(context.Context) (updatejob.Job, error) {
	if f.err != nil {
		return updatejob.Job{}, f.err
	}
	return f.job, nil
}

func updatePlanFixture() updatejob.Plan {
	return updatejob.Plan{
		ID: strings.Repeat("a", 32), CheckedAt: "2026-10-31T12:00:00Z",
		Installed: "0.1.0", Latest: "v0.2.0", Channel: "stable",
		Platform: "linux/arm64", Result: "available", Reason: "v0.2.0 is newer",
		ArchiveSHA256: strings.Repeat("b", 64), Size: 1024,
	}
}

func updateJobFixture() updatejob.Job {
	return updatejob.Job{
		ID: strings.Repeat("c", 32), Channel: "stable", From: "0.1.0", To: "v0.2.0",
		Phase: updatejob.PhaseInstalling, Result: updatejob.ResultPending,
		StartedAt: "2026-10-31T12:00:00Z", UpdatedAt: "2026-10-31T12:01:00Z",
	}
}

func updateSessions(t *testing.T, h *Handler) (updates, people string) {
	t.Helper()
	updates, _, err := h.operators.issue("Updater", false, "updates")
	if err != nil {
		t.Fatal(err)
	}
	people, _, err = h.operators.issue("People", false, "people")
	if err != nil {
		t.Fatal(err)
	}
	return updates, people
}

// installSession is a passkey-backed operator with every tool, as the first
// administrator gets. issueSliding records the fresh passkey proof.
func installSession(t *testing.T, h *Handler) string {
	t.Helper()
	token, grant, err := h.operators.issueSliding("Installer", operatorTools...)
	if err != nil {
		t.Fatal(err)
	}
	h.operators.bind(grant.ID, "installer-account", []byte("credential"))
	return token
}

func TestUpdatePlanAPINeedsTheUpdatesToolAndUsesTheExecutor(t *testing.T) {
	exec := &fakeUpdateExecutor{plan: updatePlanFixture()}
	h := New(&fakeManager{}, nil, WithUpdateExecutor(exec))
	updates, people := updateSessions(t, h)
	if rec := request(h, http.MethodGet, "/_/api/updates/plan", people, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("people plan = %d, want 403", rec.Code)
	}
	rec := request(h, http.MethodGet, "/_/api/updates/plan", updates, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"latest":"v0.2.0"`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("plan = %d %s", rec.Code, rec.Body.String())
	}
}

func TestUpdatePlanAPIWithoutAnExecutorStaysConsultationOnly(t *testing.T) {
	h := New(&fakeManager{}, nil)
	updates, _ := updateSessions(t, h)
	if rec := request(h, http.MethodGet, "/_/api/updates/plan", updates, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("plan without executor = %d, want 503", rec.Code)
	}
	installer := installSession(t, h)
	if rec := request(h, http.MethodPost, "/_/api/updates/jobs", installer, `{"plan_id":"`+strings.Repeat("a", 32)+`"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("job without executor = %d, want 503", rec.Code)
	}
}

func TestStartUpdateJobAcceptsOnlyOneReviewedPlanID(t *testing.T) {
	plan := updatePlanFixture()
	exec := &fakeUpdateExecutor{plan: plan, job: updateJobFixture()}
	h := New(&fakeManager{}, nil, WithUpdateExecutor(exec))
	_, people := updateSessions(t, h)
	installer := installSession(t, h)
	if rec := request(h, http.MethodPost, "/_/api/updates/jobs", people, `{"plan_id":"`+plan.ID+`"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("people start = %d, want 403", rec.Code)
	}
	for _, body := range []string{
		`{}`,
		`{"plan_id":"not-an-id"}`,
		`{"plan_id":"` + plan.ID + `","command":"reboot"}`,
		`{"plan_id":"` + plan.ID + `"}{}`,
	} {
		if rec := request(h, http.MethodPost, "/_/api/updates/jobs", installer, body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %s = %d, want 400", body, rec.Code)
		}
	}
	rec := request(h, http.MethodPost, "/_/api/updates/jobs", installer, `{"plan_id":"`+plan.ID+`"}`)
	if rec.Code != http.StatusAccepted || exec.started != plan.ID || !strings.Contains(rec.Body.String(), `"result":"pending"`) {
		t.Fatalf("start = %d %s, started=%q", rec.Code, rec.Body.String(), exec.started)
	}
}

func TestUpdateJobAPIsValidateIDsAndMapExecutorRefusals(t *testing.T) {
	exec := &fakeUpdateExecutor{job: updateJobFixture()}
	h := New(&fakeManager{}, nil, WithUpdateExecutor(exec))
	updates, _ := updateSessions(t, h)
	if rec := request(h, http.MethodGet, "/_/api/updates/jobs/not-an-id", updates, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("invalid id = %d, want 404", rec.Code)
	}
	if rec := request(h, http.MethodGet, "/_/api/updates/jobs/"+strings.Repeat("c", 32), updates, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"phase":"installing"`) {
		t.Fatalf("job = %d %s", rec.Code, rec.Body.String())
	}
	if rec := request(h, http.MethodGet, "/_/api/updates/jobs/latest", updates, ""); rec.Code != http.StatusOK {
		t.Fatalf("latest = %d", rec.Code)
	}

	exec.err = updateexec.ErrNoJob
	if rec := request(h, http.MethodGet, "/_/api/updates/jobs/latest", updates, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("no job = %d, want 404", rec.Code)
	}
	exec.err = &updateexec.Error{Status: http.StatusConflict, Message: "review a fresh update plan before installing"}
	installer := installSession(t, h)
	if rec := request(h, http.MethodPost, "/_/api/updates/jobs", installer, `{"plan_id":"`+strings.Repeat("a", 32)+`"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "review a fresh") {
		t.Fatalf("conflict = %d %s", rec.Code, rec.Body.String())
	}
	exec.err = fmt.Errorf("dial unix /run/secret/executor.sock: permission denied")
	rec := request(h, http.MethodGet, "/_/api/updates/plan", updates, "")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "/run/secret") {
		t.Fatalf("transport error leaked: %d %s", rec.Code, rec.Body.String())
	}
}

type captureAudit struct{ events []events.Event }

func (c *captureAudit) Append(_ context.Context, event events.Event) error {
	c.events = append(c.events, event)
	return nil
}

func TestStartUpdateJobNeedsTheSeparateToolAndAFreshPasskey(t *testing.T) {
	exec := &fakeUpdateExecutor{plan: updatePlanFixture(), job: updateJobFixture()}
	audit := &captureAudit{}
	recorder := &events.Recorder{}
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: "global"}}}, recorder,
		WithUpdateExecutor(exec), WithAdminMutationAudit(audit, true))
	body := `{"plan_id":"` + updatePlanFixture().ID + `"}`

	// The global admin token is the host's API credential, not an operator
	// passkey, and cannot replace the binary.
	if rec := request(h, http.MethodPost, "/_/api/updates/jobs", "global", body); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"operator_required"`) {
		t.Fatalf("admin token start = %d %s", rec.Code, rec.Body.String())
	}
	// The host socket is root's own path; the console route is not another
	// way to ask it to install. The CLI runs `ozal update` instead.
	localReq := httptest.NewRequest(http.MethodPost, "/_/api/updates/jobs", strings.NewReader(body))
	localReq.Host = "127.0.0.1:9090"
	localReq = localReq.WithContext(context.WithValue(localReq.Context(), localKey{}, true))
	localRec := httptest.NewRecorder()
	h.ServeHTTP(localRec, localReq)
	if localRec.Code != http.StatusForbidden || !strings.Contains(localRec.Body.String(), `"code":"operator_required"`) {
		t.Fatalf("host socket start = %d %s", localRec.Code, localRec.Body.String())
	}

	// A passkey session with only the consultation tool cannot install.
	viewer, vg, err := h.operators.issueSliding("Viewer", "updates")
	if err != nil {
		t.Fatal(err)
	}
	h.operators.bind(vg.ID, "viewer-account", []byte("credential"))
	if rec := request(h, http.MethodPost, "/_/api/updates/jobs", viewer, body); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"tool_required"`) {
		t.Fatalf("viewer start = %d %s", rec.Code, rec.Body.String())
	}

	// The install tool without a passkey proof is still refused.
	host, hg, err := h.operators.issue("Host login", false, "updates.install")
	if err != nil {
		t.Fatal(err)
	}
	h.operators.bind(hg.ID, "host-account", []byte("credential"))
	if rec := request(h, http.MethodPost, "/_/api/updates/jobs", host, body); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"passkey_required"`) {
		t.Fatalf("host login start = %d %s", rec.Code, rec.Body.String())
	}

	// A proof older than five minutes is stale.
	stale, sg, err := h.operators.issueSliding("Stale", "updates.install")
	if err != nil {
		t.Fatal(err)
	}
	h.operators.bind(sg.ID, "stale-account", []byte("credential"))
	h.operators.now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	if rec := request(h, http.MethodPost, "/_/api/updates/jobs", stale, body); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"passkey_required"`) {
		t.Fatalf("stale proof start = %d %s", rec.Code, rec.Body.String())
	}

	// A fresh passkey session with the explicit tool is accepted, and the
	// request is audited before the executor starts.
	fresh := installSession(t, h)
	rec := request(h, http.MethodPost, "/_/api/updates/jobs", fresh, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("fresh start = %d %s", rec.Code, rec.Body.String())
	}
	if len(audit.events) != 1 || audit.events[0].Code != "update.install" {
		t.Fatalf("audited intents = %+v", audit.events)
	}
	if !recorder.Has(events.Type("update.started")) {
		t.Fatal("starting an update left no event")
	}
}
