package admin

import (
	"errors"
	"net/http"
	"slices"

	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

type groupView struct {
	Name    string   `json:"name"`
	Source  string   `json:"source"`
	Members []string `json:"members"`
	Hosts   []string `json:"hosts"`
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	users, err := h.people.Users(r.Context())
	if err != nil {
		writeError(w, 503, "groups could not be read")
		return
	}
	groups := map[string]*groupView{}
	get := func(name string) *groupView {
		if groups[name] == nil {
			groups[name] = &groupView{Name: name, Source: "local", Members: []string{}, Hosts: []string{}}
		}
		return groups[name]
	}
	for _, user := range users {
		for _, name := range user.Groups {
			g := get(name)
			g.Members = append(g.Members, user.Email)
		}
	}
	for _, route := range h.manager.Config().Routes {
		for _, name := range route.Policy().RequiredGroups {
			g := get(name)
			g.Hosts = append(g.Hosts, route.Host)
		}
	}
	out := make([]groupView, 0, len(groups))
	for _, g := range groups {
		slices.Sort(g.Members)
		slices.Sort(g.Hosts)
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b groupView) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	writeJSON(w, 200, map[string]any{"groups": out, "external_groups": "OIDC membership is owned by the configured provider; these are native memberships and route requirements"})
}

func (h *Handler) setPersonGroups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expected *[]string `json:"expected"`
		Groups   *[]string `json:"groups"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.Expected == nil || req.Groups == nil {
		writeError(w, 400, "expected and groups arrays are required")
		return
	}
	next, err := sqlite.NormalizeGroups(*req.Groups)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	email := r.PathValue("email")
	if !h.recordMutationIntent(w, r, "user.groups", email) {
		return
	}
	if err := h.people.SetGroups(r.Context(), email, *req.Expected, next); err != nil {
		if errors.Is(err, sqlite.ErrMembershipChanged) {
			writeError(w, 409, "membership changed; refresh before editing")
			return
		}
		h.personError(w, err)
		return
	}
	h.emit(events.Event{Type: events.UserGroupsChanged, Actor: actor(r), Host: email, Code: "membership.updated"})
	writeJSON(w, 200, map[string]any{"email": email, "groups": next, "sessions_ended": true})
}
