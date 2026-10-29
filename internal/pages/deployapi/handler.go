package deployapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/pages"
	"github.com/ozal-project/ozal/internal/pages/deployauth"
	"github.com/ozal-project/ozal/internal/pipeline"
	"github.com/ozal-project/ozal/internal/ratelimit"
)

type Store interface {
	DeployProject(context.Context, string, pages.SourceMetadata, io.Reader) (pages.Deployment, error)
	Deployment(context.Context, string, string) (pages.Deployment, error)
}
type Verifier interface {
	Verify(context.Context, string) (deployauth.Claims, error)
}
type ReplayStore interface {
	Consume(context.Context, string, time.Time) (bool, error)
}
type Limiter interface {
	Allow(string) (bool, time.Duration)
}

type Options struct {
	Host, Audience              string
	Projects                    func() []config.Project
	Store                       Store
	Verifier                    Verifier
	Replays                     ReplayStore
	Sink                        events.Sink
	Audit                       events.AuditAppender
	AuditEnabled, AuditRequired bool
	ProjectLimiter              Limiter
	IPLimiter                   Limiter
	ResolveSecret               func(string) (string, error)
	Now                         func() time.Time
}

type Handler struct {
	options Options
	mux     *http.ServeMux
	mu      sync.Mutex
	busy    map[string]bool
}

func New(options Options) (*Handler, error) {
	host, err := config.NormalizeHost(options.Host)
	if err != nil || strings.ContainsAny(host, ":[]") || options.Audience == "" || options.Projects == nil || options.Store == nil || options.Replays == nil {
		return nil, errors.New("deploy endpoint requires a host, audience, project source, store and replay adapter")
	}
	options.Host = host
	if options.AuditRequired {
		options.AuditEnabled = true
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Sink == nil {
		options.Sink = events.Discard
	}
	if options.ResolveSecret == nil {
		options.ResolveSecret = config.ResolveSecret
	}
	if options.ProjectLimiter == nil {
		options.ProjectLimiter = ratelimit.NewLimiter(1, 5, 10*time.Minute)
	}
	if options.IPLimiter == nil {
		options.IPLimiter = ratelimit.NewLimiter(2, 10, 10*time.Minute)
	}
	h := &Handler{options: options, mux: http.NewServeMux(), busy: map[string]bool{}}
	h.mux.HandleFunc("POST /v1/projects/{project}/deployments", h.upload)
	h.mux.HandleFunc("GET /v1/projects/{project}/deployments/{id}", h.status)
	h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		h.reject(w, r, 404, "deployment.not_found", config.Project{}, identity{})
	})
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	host, err := requestHostname(r.Host)
	if err != nil || host != h.options.Host {
		h.reject(w, r, 404, "deployment.not_found", config.Project{}, identity{})
		return
	}
	key := "unknown"
	if ip, ok := pipeline.ClientIP(r.Context()); ok {
		key = ip.String()
	} else if peer, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if ip := net.ParseIP(peer); ip != nil {
			key = ip.String()
		}
	}
	if allowed, _ := h.options.IPLimiter.Allow(key); !allowed {
		h.reject(w, r, 429, "deployment.ip_rate_limited", config.Project{}, identity{})
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		h.reject(w, r, 405, "deployment.method", config.Project{}, identity{})
		return
	}
	if path.Clean(r.URL.Path) != r.URL.Path {
		h.reject(w, r, 404, "deployment.not_found", config.Project{}, identity{})
		return
	}
	h.mux.ServeHTTP(w, r)
}

// RouteHost consumes every path on the dedicated host, including unknown paths,
// so an admin or application endpoint cannot accidentally appear there.
func RouteHost(host string, deploy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, err := requestHostname(r.Host)
		if err != nil {
			r.Close = true
			w.Header().Set("Connection", "close")
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		if key == host {
			deploy.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestHostname(raw string) (string, error) {
	host, err := config.NormalizeHost(raw)
	if err != nil {
		return "", err
	}
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name, nil
	}
	return host, nil
}

// admit authenticates the caller for one project. A mutation also spends the
// project's deploy budget and the OIDC token's one-time jti; a status read
// does neither, so a workflow can poll with the token that uploaded.
func (h *Handler) admit(w http.ResponseWriter, r *http.Request, mutation bool) (config.Project, identity, bool) {
	var project config.Project
	for _, candidate := range h.options.Projects() {
		if candidate.Name == r.PathValue("project") {
			project = candidate
			break
		}
	}
	if project.Name == "" {
		h.reject(w, r, 404, "project.not_found", project, identity{})
		return project, identity{}, false
	}
	id := identity{source: events.DeploymentSource{Project: project.Name}}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		h.reject(w, r, 401, "deployer.token_invalid", project, id)
		return project, id, false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || len(token) > 16<<10 || strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		h.reject(w, r, 401, "deployer.token_invalid", project, id)
		return project, id, false
	}
	id, code, status := h.authenticate(r.Context(), project, token)
	if status != 0 {
		h.reject(w, r, status, code, project, id)
		return project, id, false
	}
	if !mutation {
		return project, id, true
	}
	if allowed, wait := h.options.ProjectLimiter.Allow(project.Name); !allowed {
		seconds := int64((wait + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		h.reject(w, r, 429, "deployment.rate_limited", project, id)
		return project, id, false
	}
	if id.claims != nil {
		consumed, err := h.options.Replays.Consume(r.Context(), id.claims.JWTID, id.claims.ExpiresAt)
		if err != nil {
			h.reject(w, r, 503, "deployer.replay_unavailable", project, id)
			return project, id, false
		}
		if !consumed {
			h.reject(w, r, 401, "deployer.replay", project, id)
			return project, id, false
		}
	}
	return project, id, true
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request, status int, code string, project config.Project, id identity) {
	// Stop net/http from draining a rejected upload after this handler returns.
	r.Close = true
	w.Header().Set("Connection", "close")
	if status == 401 {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	// Requests that never named a configured project (wrong host, IP limit,
	// method, path, unknown project) are internet background noise: they get
	// their stable code in the response but no activity event, so scanners
	// cannot flood the operator's feed.
	if project.Name == "" {
		writeJSON(w, status, map[string]string{"code": code, "error": "deployment request refused"})
		return
	}
	copy := id.source
	copy.Project = project.Name
	source := &copy
	h.options.Sink.Emit(events.Event{Time: h.options.Now().UTC(), Type: events.PagesDeployRefused, Host: project.Site, Actor: id.actor, Code: code, Reason: "deployment request refused", Deployment: source})
	writeJSON(w, status, map[string]string{"code": code, "error": "deployment request refused"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
