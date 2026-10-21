// Package events defines what Switchboard publishes and how an event reaches
// a sink. Types are stable: renaming one is a breaking change for whoever
// consumes the JSON logs.
package events

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Type is a stable event name.
type Type string

const (
	AccessAllowed     Type = "access.allowed"
	AccessDenied      Type = "access.denied"
	AccessRateLimited Type = "access.rate_limited"
	// An allowed visitor was sent to log in again because the route's
	// max_auth_age had passed (ID-005 step-up).
	AccessStepUp   Type = "access.step_up"
	LoginStarted   Type = "login.started"
	LoginFailed    Type = "login.failed"
	LoginSucceeded Type = "login.succeeded"
	RouteCreated   Type = "route.created"
	RouteDeleted   Type = "route.deleted"
	ConfigReloaded Type = "config.reloaded"
	// Deployments of static sites (PAGES-002). Host carries the site name.
	PagesDeployed   Type = "pages.deployed"
	PagesRolledBack Type = "pages.rolled_back"
	PagesRefused    Type = "pages.refused"
	// A user enrolled a passkey (ID-003). Code is "synced" or "device_bound".
	PasskeyRegistered Type = "passkey.registered"
	// A passkey was revoked, by its user from a session (ID-008) or by the
	// operator; Reason says which.
	PasskeyRevoked Type = "passkey.revoked"
	// Operator changes to people from the console. Host carries the user's
	// email; an invitation's token is never part of an event.
	UserInvited  Type = "user.invited"
	UserDisabled Type = "user.disabled"
	UserEnabled  Type = "user.enabled"
)

// Event is one decision or change with the context needed to explain it.
type Event struct {
	Time       time.Time
	Type       Type
	Actor      string // principal subject, or "admin" / "signal:SIGHUP"
	Host       string
	Code       string // stable decision code, e.g. auth.Decision.Code
	Reason     string
	Error      string
	RemoteAddr string
	// Count, when > 0, summarises repeated identical events that were
	// suppressed inside an aggregation window (see originauth denials).
	Count int
	// ConfigSHA256 is set on config.reloaded: the digest of the config file
	// in effect afterwards (the previous one when the reload failed).
	ConfigSHA256 string
}

// Sink receives events. Emit must not block: callers are on the request path
// or inside the control plane.
type Sink interface {
	Emit(Event)
}

// Discard drops every event; it is the neutral default.
var Discard Sink = discardSink{}

type discardSink struct{}

func (discardSink) Emit(Event) {}

// SlogSink writes each event as one JSON record through log/slog.
type SlogSink struct {
	Logger *slog.Logger
}

func (s SlogSink) Emit(e Event) {
	if s.Logger == nil {
		return
	}
	attrs := make([]slog.Attr, 0, 7)
	for _, field := range []struct{ key, value string }{
		{"actor", e.Actor},
		{"host", e.Host},
		{"code", e.Code},
		{"reason", e.Reason},
		{"error", e.Error},
		{"remote_addr", e.RemoteAddr},
		{"config_sha256", e.ConfigSHA256},
	} {
		if field.value != "" {
			attrs = append(attrs, slog.String(field.key, field.value))
		}
	}
	if e.Count > 0 {
		attrs = append(attrs, slog.Int("count", e.Count))
	}
	s.Logger.LogAttrs(context.Background(), slog.LevelInfo, string(e.Type), attrs...)
}

// Recorder is an in-memory sink for tests and local inspection.
type Recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *Recorder) Emit(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

// Has reports whether the recorder captured an event of the given type.
func (r *Recorder) Has(t Type) bool {
	for _, e := range r.Events() {
		if e.Type == t {
			return true
		}
	}
	return false
}

// Find returns the first captured event of the given type.
func (r *Recorder) Find(t Type) (Event, bool) {
	for _, e := range r.Events() {
		if e.Type == t {
			return e, true
		}
	}
	return Event{}, false
}
