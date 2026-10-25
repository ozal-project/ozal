package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
)

type recordingMutationAudit struct {
	events []events.Event
	err    error
}

func (a *recordingMutationAudit) Append(_ context.Context, event events.Event) error {
	a.events = append(a.events, event)
	return a.err
}

func TestRequiredAdminAuditFailureStopsRouteMutation(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	live := &events.Recorder{}
	audit := &recordingMutationAudit{err: errors.New("disk full at /private/audit")}
	handler := New(manager, live, WithAdminMutationAudit(audit, true))

	rec := request(handler, http.MethodPost, "/routes", "secret", `{"host":"new.localhost","target":"http://localhost:9001"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("mutation after required audit failure = %d, want 503", rec.Code)
	}
	if len(manager.added) != 0 {
		t.Fatalf("audit failure reached route manager: %+v", manager.added)
	}
	if len(live.Events()) != 0 {
		t.Fatalf("failed required intent appeared in live events: %+v", live.Events())
	}
	if len(audit.events) != 1 || audit.events[0].Type != events.AdminMutationRequested || audit.events[0].Code != "route.create" || audit.events[0].Host != "new.localhost" {
		t.Fatalf("audit intent = %+v", audit.events)
	}
	if containsAny(rec.Body.String(), "disk full", "/private/audit") {
		t.Fatalf("response leaked audit storage details: %s", rec.Body)
	}
}

func TestBestEffortAdminAuditFailureAllowsRouteMutation(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	live := &events.Recorder{}
	audit := &recordingMutationAudit{err: errors.New("disk full")}
	handler := New(manager, live, WithAdminMutationAudit(audit, false))

	rec := request(handler, http.MethodPost, "/routes", "secret", `{"host":"new.localhost","target":"http://localhost:9001"}`)
	if rec.Code != http.StatusCreated || len(manager.added) != 1 {
		t.Fatalf("best-effort mutation = %d, routes=%+v, want created", rec.Code, manager.added)
	}
	if len(audit.events) != 1 || audit.events[0].Type != events.AdminMutationRequested || audit.events[0].Code != "route.create" {
		t.Fatalf("audit intent = %+v", audit.events)
	}
	if !live.Has(events.AdminMutationRequested) || !live.Has(events.RouteCreated) {
		t.Fatalf("live events = %+v; want intent and outcome", live.Events())
	}
	if containsAny(rec.Body.String(), "disk full") {
		t.Fatalf("best-effort response leaked the storage error: %s", rec.Body)
	}
	for _, event := range live.Events() {
		if event.Error != "" || strings.Contains(event.Reason, "disk full") {
			t.Fatalf("best-effort live event leaked the storage error: %+v", event)
		}
	}
}

func TestRequiredAdminAuditFailureStopsPeopleSiteAndConnectorMutations(t *testing.T) {
	t.Run("people invitation", func(t *testing.T) {
		audit := &recordingMutationAudit{err: errors.New("disk full")}
		handler, identity, _ := peopleHandler(t, WithAdminMutationAudit(audit, true))
		rec := request(handler, http.MethodPost, "/_/api/people/invite", "secret", `{"email":"ana@example.com"}`)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("invite after required audit failure = %d, want 503", rec.Code)
		}
		users, err := identity.Users(context.Background())
		if err != nil || len(users) != 0 {
			t.Fatalf("people mutation reached identity store: users=%+v err=%v", users, err)
		}
	})

	t.Run("site deployment", func(t *testing.T) {
		audit := &recordingMutationAudit{err: errors.New("disk full")}
		store := &fakeDeployments{}
		handler := sitesHandler(store, &events.Recorder{}, WithAdminMutationAudit(audit, true))
		rec := request(handler, http.MethodPost, "/_/api/sites/blog/deployments", "secret", "archive")
		if rec.Code != http.StatusServiceUnavailable || len(store.received) != 0 {
			t.Fatalf("deploy after required audit failure = %d, store=%v; want 503 and no call", rec.Code, store.received)
		}
	})

	t.Run("connector enrollment", func(t *testing.T) {
		audit := &recordingMutationAudit{err: errors.New("disk full")}
		handler, _ := connectorHandler(t, WithAdminMutationAudit(audit, true))
		certificate, _ := certificatePEM(t)
		body, err := json.Marshal(map[string]string{"name": "portfolio", "certificate_pem": certificate})
		if err != nil {
			t.Fatal(err)
		}
		rec := local(handler, http.MethodPost, "/_/api/connectors", string(body))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("enroll after required audit failure = %d, want 503", rec.Code)
		}
		if current := request(handler, http.MethodGet, "/_/api/connectors/portfolio", "secret", ""); current.Code != http.StatusNotFound {
			t.Fatalf("connector exists after refused enrollment: %d %s", current.Code, current.Body)
		}
		if len(audit.events) != 1 || audit.events[0].Type != events.AdminMutationRequested || audit.events[0].Code != "connector.enroll" || audit.events[0].Host != "portfolio" {
			t.Fatalf("connector intent = %+v", audit.events)
		}
	})
}

func containsAny(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func hasMutationCode(recorded []events.Event, code string) bool {
	for _, event := range recorded {
		if event.Type == events.AdminMutationRequested && event.Code == code {
			return true
		}
	}
	return false
}
