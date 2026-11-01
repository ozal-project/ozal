package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/accesshistory"
	"github.com/ozal-project/ozal/internal/events"
)

func accessFixture() *accesshistory.Store {
	store := accesshistory.NewStore(accesshistory.Journal, time.Time{}, 100, 100)
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for i, e := range []events.Event{
		{Type: events.AccessAllowed, Actor: "sb:ana", Host: "grafana.example.com", Code: "allowed"},
		{Type: events.AccessDenied, Actor: "sb:bob", Host: "grafana.example.com", Code: "email_not_allowed", Reason: "bob@other.org is not allowed"},
		{Type: events.AccessAllowed, Actor: "sb:ana", Host: "wiki.example.com", Code: "allowed"},
	} {
		e.Time = start.Add(time.Duration(i) * time.Minute)
		store.Add(e)
	}
	return store
}

// readerSession holds a tool unrelated to access: reading what the console
// shows needs any operator session.
func readerSession(t *testing.T, h *Handler) string {
	t.Helper()
	token, _, err := h.operators.issue("Reader", false, "sites")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestAccessHistoryAnswersAnyOperatorWithSummaryAndEntries(t *testing.T) {
	h := New(&fakeManager{}, nil, WithAccessHistory(accessFixture()))
	token := readerSession(t, h)
	if rec := request(h, http.MethodGet, "/_/api/access", "", ""); rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "sb:bob") {
		t.Fatalf("no credential = %d %s, want a refusal", rec.Code, rec.Body.String())
	}
	rec := request(h, http.MethodGet, "/_/api/access?outcome=refused", token, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("access = %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Summary accesshistory.Summary `json:"summary"`
		Entries []accesshistory.Entry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Summary.Source != accesshistory.Journal || got.Summary.Allowed != 2 || got.Summary.Refused != 1 || len(got.Summary.People) != 2 {
		t.Fatalf("summary = %+v", got.Summary)
	}
	if len(got.Entries) != 1 || got.Entries[0].Actor != "sb:bob" || got.Entries[0].Code != "email_not_allowed" {
		t.Fatalf("entries = %+v, want only the refusal", got.Entries)
	}
}

func TestAccessHistoryRefusesUnusableFilters(t *testing.T) {
	h := New(&fakeManager{}, nil, WithAccessHistory(accessFixture()))
	token := readerSession(t, h)
	for _, query := range []string{"outcome=maybe", "before=0", "before=x", "limit=0", "limit=201", "host=" + strings.Repeat("a", 255), "actor=a%00b"} {
		if rec := request(h, http.MethodGet, "/_/api/access?"+query, token, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", query, rec.Code)
		}
	}
}

func TestAccessHistoryIsAbsentWithoutAStore(t *testing.T) {
	h := New(&fakeManager{}, nil)
	if rec := request(h, http.MethodGet, "/_/api/access", readerSession(t, h), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("access without a store = %d, want 404", rec.Code)
	}
}
