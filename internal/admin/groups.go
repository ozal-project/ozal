package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/store/sqlite"
)

type groupView struct {
	Name    string   `json:"name"`
	Source  string   `json:"source"`
	Members []string `json:"members"`
	Hosts   []string `json:"hosts"`
	// Defined says an operator created it on purpose; it then exists even
	// with no members and no application requiring it.
	Defined bool `json:"defined,omitempty"`
}

// groupStore is the catalog of groups created on purpose (sqlite.GroupCatalog).
type groupStore interface {
	Defined(ctx context.Context) ([]string, error)
	Create(ctx context.Context, name string) (string, error)
	Delete(ctx context.Context, name string) error
}

// WithGroupCatalog lets operators create a group before anyone is in it.
func WithGroupCatalog(store groupStore) Option {
	return func(h *Handler) { h.groupCatalog = store }
}

// groupsView is every group the edge knows: created on purpose, held by a
// member, or required by an application.
func (h *Handler) groupsView(ctx context.Context) ([]groupView, error) {
	users, err := h.people.Users(ctx)
	if err != nil {
		return nil, err
	}
	groups := map[string]*groupView{}
	get := func(name string) *groupView {
		if groups[name] == nil {
			groups[name] = &groupView{Name: name, Source: "local", Members: []string{}, Hosts: []string{}}
		}
		return groups[name]
	}
	if h.groupCatalog != nil {
		defined, err := h.groupCatalog.Defined(ctx)
		if err != nil {
			return nil, err
		}
		for _, name := range defined {
			get(name).Defined = true
		}
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
	slices.SortFunc(out, func(a, b groupView) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	out, err := h.groupsView(r.Context())
	if err != nil {
		writeError(w, 503, "groups could not be read")
		return
	}
	writeJSON(w, 200, map[string]any{"groups": out, "can_create": h.groupCatalog != nil, "external_groups": "OIDC membership is owned by the configured provider; these are native memberships and route requirements"})
}

// createGroup defines an empty group, so people can be invited into it and
// applications can require it before anyone belongs to it.
func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	if h.groupCatalog == nil {
		writeError(w, 503, "creating groups needs the state database")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid group request")
		return
	}
	names, err := sqlite.NormalizeGroups([]string{req.Name})
	if err != nil || strings.Contains(names[0], ",") {
		writeError(w, 400, "group names must contain 1 to 128 bytes, without commas or control characters")
		return
	}
	known, err := h.groupsView(r.Context())
	if err != nil {
		writeError(w, 503, "groups could not be read")
		return
	}
	for _, g := range known {
		if g.Name == names[0] {
			writeError(w, 409, "this group already exists")
			return
		}
	}
	if !h.recordMutationIntent(w, r, "group.create", "") {
		return
	}
	name, err := h.groupCatalog.Create(r.Context(), names[0])
	if errors.Is(err, sqlite.ErrGroupExists) {
		writeError(w, 409, "this group already exists")
		return
	}
	if err != nil {
		writeError(w, 500, "the group could not be created")
		return
	}
	h.emit(events.Event{Type: events.Type("group.created"), Actor: actor(r), Code: name})
	writeJSON(w, 201, map[string]string{"name": name})
}

// deleteGroup removes an unused group: one with members or required by an
// application is refused, so access never changes through this call.
func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if h.groupCatalog == nil {
		writeError(w, 503, "deleting groups needs the state database")
		return
	}
	name := r.PathValue("name")
	known, err := h.groupsView(r.Context())
	if err != nil {
		writeError(w, 503, "groups could not be read")
		return
	}
	for _, g := range known {
		if g.Name != name {
			continue
		}
		if len(g.Members) > 0 || len(g.Hosts) > 0 {
			writeError(w, 409, "remove its members and the applications that require it first")
			return
		}
		if !g.Defined {
			break
		}
		if !h.recordMutationIntent(w, r, "group.delete", "") {
			return
		}
		if err := h.groupCatalog.Delete(r.Context(), name); err != nil {
			writeError(w, 500, "the group could not be deleted")
			return
		}
		h.emit(events.Event{Type: events.Type("group.deleted"), Actor: actor(r), Code: name})
		writeJSON(w, 200, map[string]string{"deleted": name})
		return
	}
	writeError(w, 404, "no such group")
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
