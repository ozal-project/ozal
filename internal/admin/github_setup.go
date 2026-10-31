package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/githubapp"
)

// Connecting GitHub from the console (PAGES-010): the operator's browser
// creates a private App from a manifest, GitHub sends it back here with a
// one-use code, and this edge exchanges the code for the App's key. The key is
// written once to a 0600 file beside the state database; the configuration
// keeps only its file: reference, like a key placed there by hand.

type githubSetup interface {
	ConvertManifest(context.Context, string) (githubapp.CreatedApp, error)
	AppStatus(context.Context, config.GitHubApp) (githubapp.AppStatus, error)
}

type githubConfigurer interface {
	ConfigureGitHub(*config.GitHubApp, string) error
}

// WithGitHubKeyDir lets the console connect GitHub, keeping App keys in dir.
// Without it, connecting stays a host operation (`ozal github connect`).
func WithGitHubKeyDir(dir string) Option {
	return func(h *Handler) { h.githubKeyDir = dir }
}

const manifestStateTTL = time.Hour

// manifestStates binds GitHub's redirect to the session that started it. A
// code arriving with an unknown, reused, expired or another session's state
// is refused: otherwise a link could connect an App someone else controls.
type manifestStates struct {
	mu      sync.Mutex
	pending map[string]manifestState
}

type manifestState struct {
	actor   string
	expires time.Time
}

func (s *manifestStates) start(actor string, now time.Time) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = map[string]manifestState{}
	}
	for key, p := range s.pending {
		if now.After(p.expires) {
			delete(s.pending, key)
		}
	}
	if len(s.pending) >= 16 {
		return "", errors.New("too many GitHub connections in progress; finish or wait for one to expire")
	}
	s.pending[state] = manifestState{actor: actor, expires: now.Add(manifestStateTTL)}
	return state, nil
}

// take consumes a state; it succeeds once, for the session that started it.
func (s *manifestStates) take(state, actor string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[state]
	if !ok {
		return false
	}
	delete(s.pending, state)
	return p.actor == actor && !now.After(p.expires)
}

func (h *Handler) setupGitHub() (githubSetup, bool) {
	setup, ok := h.github.(githubSetup)
	return setup, ok
}

func (h *Handler) githubManifest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Origin       string `json:"origin"`
		Organization string `json:"organization"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid GitHub connection request")
		return
	}
	if h.manager.Config().GitHub != nil {
		writeError(w, 409, "GitHub is already connected")
		return
	}
	if _, ok := h.setupGitHub(); !ok || h.githubKeyDir == "" {
		writeError(w, 422, "connecting GitHub here needs the state database (-db); on the host use `ozal github connect`")
		return
	}
	state, err := h.githubStates.start(actor(r), time.Now())
	if err != nil {
		writeError(w, 429, err.Error())
		return
	}
	target, manifest, err := githubapp.Manifest(githubapp.ManifestRequest{Name: "Ozal " + state[:6], Origin: strings.TrimSpace(req.Origin), Organization: strings.TrimSpace(req.Organization), State: state})
	if err != nil {
		h.githubStates.take(state, actor(r), time.Now())
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"url": target, "manifest": manifest})
}

func (h *Handler) githubManifestConversion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code     string `json:"code"`
		State    string `json:"state"`
		Expected string `json:"expected"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid GitHub connection request")
		return
	}
	if !h.githubStates.take(req.State, actor(r), time.Now()) {
		writeError(w, 403, "this GitHub connection was not started from this session, was already used or expired; start again")
		return
	}
	setup, ok := h.setupGitHub()
	writer, writable := h.manager.(githubConfigurer)
	if !ok || !writable || h.githubKeyDir == "" {
		writeError(w, 422, "connecting GitHub here needs the state database (-db); on the host use `ozal github connect`")
		return
	}
	if req.Expected == "" || req.Expected != h.manager.ConfigDigest() {
		writeError(w, 409, "configuration changed; start connecting GitHub again")
		return
	}
	if h.manager.Config().GitHub != nil {
		writeError(w, 409, "GitHub is already connected")
		return
	}
	created, err := setup.ConvertManifest(r.Context(), req.Code)
	if err != nil {
		writeError(w, 503, "GitHub did not confirm the new App; if it appears in your GitHub settings, delete it and start again")
		return
	}
	// The exchange is network I/O: a session revoked meanwhile writes nothing.
	h.requireTool("publishing", func(w http.ResponseWriter, r *http.Request) {
		if !h.recordMutationIntent(w, r, "github.connect", created.Slug) {
			return
		}
		path, err := writeGitHubKey(h.githubKeyDir, created)
		if err != nil {
			writeError(w, 500, fmt.Sprintf("the App's key could not be stored; delete the App %s in GitHub and start again", created.Slug))
			return
		}
		app := &config.GitHubApp{AppID: created.ID, PrivateKey: "file:" + path}
		if err := writer.ConfigureGitHub(app, req.Expected); err != nil {
			_ = os.Remove(path)
			writeError(w, 409, fmt.Sprintf("configuration changed while connecting; delete the App %s in GitHub and start again", created.Slug))
			return
		}
		h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Host: created.Slug, Code: "github.connected", ConfigSHA256: h.manager.ConfigDigest()})
		status := githubapp.AppStatus{Slug: created.Slug, Owner: created.Owner}
		writeJSON(w, 200, map[string]any{"slug": created.Slug, "owner": created.Owner, "install_url": status.InstallURL()})
	})(w, r)
}

// writeGitHubKey creates the key file; it never replaces an existing one.
func writeGitHubKey(dir string, created githubapp.CreatedApp) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(abs, fmt.Sprintf("github-app-%d.pem", created.ID))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(created.Key); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// githubInstallation records where the App was installed. The installation is
// read from GitHub with the App's own key, never taken from the browser: only
// an installation of this App can be bound.
func (h *Handler) githubInstallation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expected string `json:"expected"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid GitHub installation request")
		return
	}
	cfg := h.manager.Config().GitHub
	setup, ok := h.setupGitHub()
	writer, writable := h.manager.(githubConfigurer)
	if cfg == nil || !ok || !writable {
		writeError(w, 422, "connect GitHub first")
		return
	}
	if cfg.Installed() {
		writeJSON(w, 200, map[string]bool{"installed": true})
		return
	}
	if req.Expected == "" || req.Expected != h.manager.ConfigDigest() {
		writeError(w, 409, "configuration changed; refresh and check again")
		return
	}
	status, err := setup.AppStatus(r.Context(), *cfg)
	if err != nil {
		writeError(w, 503, "GitHub could not be asked where the App is installed; try again")
		return
	}
	if status.Installation == 0 {
		writeJSON(w, 409, map[string]any{"error": "the App is not installed yet: choose its repositories on GitHub", "install_url": status.InstallURL()})
		return
	}
	h.requireTool("publishing", func(w http.ResponseWriter, r *http.Request) {
		if !h.recordMutationIntent(w, r, "github.install", status.Slug) {
			return
		}
		next := *cfg
		next.InstallationID = status.Installation
		if err := writer.ConfigureGitHub(&next, req.Expected); err != nil {
			writeError(w, 409, "configuration changed; refresh and check again")
			return
		}
		h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Host: status.Slug, Code: "github.installed", ConfigSHA256: h.manager.ConfigDigest()})
		writeJSON(w, 200, map[string]bool{"installed": true})
	})(w, r)
}

// githubWorkflowPreview shows the workflow a new website will get before
// anything is saved. Saving later runs the same generator on the saved
// project; the pull request is refused unless the digests match.
func (h *Handler) githubWorkflowPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Project      string `json:"project"`
		RepositoryID int64  `json:"repository_id"`
		Page         int    `json:"page"`
		Ref          string `json:"ref"`
		Workflow     string `json:"workflow"`
		Hostname     string `json:"hostname"`
		Directory    string `json:"directory"`
		Output       string `json:"output"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid workflow request")
		return
	}
	cfg := h.manager.Config()
	if !cfg.GitHub.Installed() || h.github == nil {
		writeError(w, 422, "connect GitHub and choose its repositories first")
		return
	}
	if !validWorkflowFile(req.Workflow) {
		writeError(w, 400, "workflow must be a .yml or .yaml filename")
		return
	}
	if req.Page == 0 {
		req.Page = 1
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	listing, err := h.github.Repositories(ctx, *cfg.GitHub, req.Page)
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	var selected githubapp.Repository
	for _, repository := range listing.Repositories {
		if repository.ID == req.RepositoryID {
			selected = repository
		}
	}
	if selected.ID == 0 {
		writeError(w, 403, "repository is no longer available to this installation")
		return
	}
	plan, status, message := publishingPlan(cfg, req.Project, selected, req.Ref, req.Workflow, req.Hostname, req.Directory, req.Output)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	writeJSON(w, 200, map[string]any{"plan": plan})
}

func validWorkflowFile(name string) bool {
	return !strings.ContainsAny(name, "/\\@") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml"))
}
