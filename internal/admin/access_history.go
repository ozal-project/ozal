package admin

import (
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/ozal-project/ozal/internal/accesshistory"
)

// accessHistory is the store of access decisions behind the console's
// "who got in" and access log (UI-005).
type accessHistory interface {
	Query(accesshistory.Filter) accesshistory.Page
	Summary(people int) accesshistory.Summary
}

// WithAccessHistory serves GET /_/api/access from history. Without it the
// endpoint answers 404 and the console keeps its live, in-memory counts.
func WithAccessHistory(history accessHistory) Option {
	return func(h *Handler) { h.accessHistory = history }
}

const summaryPeople = 20

type accessView struct {
	Summary accesshistory.Summary `json:"summary"`
	accesshistory.Page
}

// listAccess answers who reached which protected application and why. Every
// operator may read it, like the event stream; it changes nothing.
func (h *Handler) listAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.accessHistory == nil {
		writeError(w, http.StatusNotFound, "access history is not available on this edge")
		return
	}
	filter, msg := accessFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	writeJSON(w, http.StatusOK, accessView{Summary: h.accessHistory.Summary(summaryPeople), Page: h.accessHistory.Query(filter)})
}

// accessFilter reads the query string; an unusable value is refused rather
// than ignored, so a typo never looks like an empty history.
func accessFilter(r *http.Request) (accesshistory.Filter, string) {
	q := r.URL.Query()
	f := accesshistory.Filter{Host: q.Get("host"), Actor: q.Get("actor"), Outcome: accesshistory.Outcome(q.Get("outcome"))}
	for name, value := range map[string]string{"host": f.Host, "actor": f.Actor} {
		if len(value) > 254 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return f, name + " is not a valid filter"
		}
	}
	if f.Outcome != "" && !accesshistory.ValidOutcome(f.Outcome) {
		return f, "outcome must be allowed, refused, step_up or rate_limited"
	}
	if raw := q.Get("before"); raw != "" {
		before, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || before == 0 {
			return f, "before must be a positive entry number"
		}
		f.Before = before
	}
	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > accesshistory.MaxLimit {
			return f, "limit must be between 1 and " + strconv.Itoa(accesshistory.MaxLimit)
		}
		f.Limit = limit
	}
	return f, ""
}
