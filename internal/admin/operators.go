package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/octaviosantacruz/switchboard/internal/events"
)

type operatorKey struct{}

// operatorTools are the console's areas of authority. A host login names the
// tools its session may change; an unnamed login grants all of them. Reading
// what the console shows is not a tool: it needs any operator session.
var operatorTools = []string{"applications", "connectors", "people", "publishing", "sites"}

type operatorGrant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Expires   time.Time `json:"expires"`
	Tools     []string  `json:"tools"`
	bootstrap bool
}

func (g operatorGrant) allows(tool string) bool { return slices.Contains(g.Tools, tool) }

// normalizeTools validates requested tools against the known set. No request
// means every tool; an unknown name is an error, never silently dropped.
func normalizeTools(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return slices.Clone(operatorTools), nil
	}
	out := make([]string, 0, len(requested))
	for _, t := range requested {
		t = strings.TrimSpace(t)
		if !slices.Contains(operatorTools, t) {
			return nil, errors.New("unknown tool " + strconv.Quote(t))
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out, nil
}

type operatorSessions struct {
	mu     sync.Mutex
	now    func() time.Time
	grants map[[32]byte]operatorGrant
}

func newOperatorSessions() *operatorSessions {
	return &operatorSessions{now: time.Now, grants: make(map[[32]byte]operatorGrant)}
}
func (s *operatorSessions) purge() {
	for key, g := range s.grants {
		if !s.now().Before(g.Expires) {
			delete(s.grants, key)
		}
	}
}
func (s *operatorSessions) issue(name string, bootstrap bool, tools ...string) (string, operatorGrant, error) {
	granted, err := normalizeTools(tools)
	if err != nil {
		return "", operatorGrant{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	if len(s.grants) >= 64 {
		return "", operatorGrant{}, errors.New("operator session capacity reached")
	}
	return s.newGrant(name, bootstrap, granted)
}
func (s *operatorSessions) newGrant(name string, bootstrap bool, tools []string) (string, operatorGrant, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", operatorGrant{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	duration := 15 * time.Minute
	if bootstrap {
		duration = 5 * time.Minute
	}
	g := operatorGrant{ID: hex.EncodeToString(hash[:8]), Name: name, Expires: s.now().Add(duration), Tools: tools, bootstrap: bootstrap}
	s.grants[hash] = g
	return token, g, nil
}
func (s *operatorSessions) exchange(token string) (string, operatorGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	hash := sha256.Sum256([]byte(token))
	g, ok := s.grants[hash]
	if !ok || !g.bootstrap {
		return "", operatorGrant{}, errors.New("invalid or expired host login")
	}
	delete(s.grants, hash)
	token, session, err := s.newGrant(g.Name, false, g.Tools)
	if err != nil {
		return "", operatorGrant{}, err
	}
	session.ID = g.ID
	s.grants[sha256.Sum256([]byte(token))] = session
	return token, session, nil
}
func (s *operatorSessions) authenticate(token string) (operatorGrant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	g, ok := s.grants[sha256.Sum256([]byte(token))]
	return g, ok && !g.bootstrap
}
func (s *operatorSessions) revoke(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, g := range s.grants {
		if g.ID == id {
			delete(s.grants, hash)
		}
	}
}

func (h *Handler) registerOperators() {
	h.mux.HandleFunc("POST /_/api/operators", h.requireLocal(h.issueOperator))
	h.mux.HandleFunc("POST /_/api/operators/exchange", h.exchangeOperator)
	h.mux.HandleFunc("GET /_/api/operators", h.requireLocal(h.listOperators))
	h.mux.HandleFunc("DELETE /_/api/operators/{id}", h.requireLocal(h.revokeOperator))
	h.mux.HandleFunc("POST /_/api/operators/logout", h.requireBearer(h.logoutOperator))
}
func (h *Handler) issueOperator(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string   `json:"name"`
		Tools []string `json:"tools"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "operator name required")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 80 || strings.ContainsFunc(req.Name, unicode.IsControl) {
		writeError(w, 400, "operator name must be 1 to 80 bytes without control characters")
		return
	}
	if !h.recordMutationIntent(w, r, "operator.login", req.Name) {
		return
	}
	if _, err := normalizeTools(req.Tools); err != nil {
		writeError(w, 400, err.Error()+"; tools: "+strings.Join(operatorTools, ", "))
		return
	}
	token, g, err := h.operators.issue(req.Name, true, req.Tools...)
	if err != nil {
		writeError(w, 503, "host login could not be issued")
		return
	}
	h.emit(events.Event{Type: events.Type("operator.login_issued"), Actor: "local", Host: g.Name, Code: g.ID, Reason: "tools: " + strings.Join(g.Tools, ",")})
	writeJSON(w, 201, map[string]any{"token": token, "id": g.ID, "expires": g.Expires})
}
func (h *Handler) exchangeOperator(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if r.Header.Get("Origin") != scheme+"://"+r.Host {
		writeError(w, 403, "open the login on this private console origin")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &req); err != nil || len(req.Token) > 128 {
		writeError(w, 400, "invalid host login")
		return
	}
	token, g, err := h.operators.exchange(req.Token)
	if err != nil {
		writeError(w, 401, "host login expired or already used; issue a new one on the host")
		return
	}
	h.emit(events.Event{Type: events.Type("operator.signed_in"), Actor: "operator:" + g.Name, Code: g.ID})
	writeJSON(w, 200, map[string]any{"token": token, "name": g.Name, "id": g.ID, "expires": g.Expires, "tools": g.Tools})
}
func (h *Handler) listOperators(w http.ResponseWriter, r *http.Request) {
	h.operators.mu.Lock()
	defer h.operators.mu.Unlock()
	h.operators.purge()
	out := []operatorGrant{}
	for _, g := range h.operators.grants {
		out = append(out, g)
	}
	writeJSON(w, 200, map[string]any{"sessions": out})
}
func (h *Handler) revokeOperator(w http.ResponseWriter, r *http.Request) {
	if !h.recordMutationIntent(w, r, "operator.revoke", r.PathValue("id")) {
		return
	}
	h.operators.revoke(r.PathValue("id"))
	h.emit(events.Event{Type: events.Type("operator.revoked"), Actor: "local", Code: r.PathValue("id")})
	writeJSON(w, 200, map[string]bool{"revoked": true})
}
func (h *Handler) logoutOperator(w http.ResponseWriter, r *http.Request) {
	if g, ok := r.Context().Value(operatorKey{}).(operatorGrant); ok {
		h.operators.revoke(g.ID)
		h.emit(events.Event{Type: events.Type("operator.signed_out"), Actor: "operator:" + g.Name, Code: g.ID})
	}
	writeJSON(w, 200, map[string]bool{"signed_out": true})
}
func (h *Handler) withOperator(r *http.Request) (*http.Request, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return r, false
	}
	grant, ok := h.operators.authenticate(token)
	if !ok {
		return r, false
	}
	return r.WithContext(context.WithValue(r.Context(), operatorKey{}, grant)), true
}
