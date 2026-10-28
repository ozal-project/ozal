// Package events defines what Switchboard publishes and how an event reaches
// a sink. Types are stable: renaming one is a breaking change for whoever
// consumes the JSON logs.
package events

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/policycontext"
)

// Type is a stable event name.
type Type string

const (
	AccessAllowed     Type = "access.allowed"
	AccessDenied      Type = "access.denied"
	AccessRateLimited Type = "access.rate_limited"
	// An operator repaired an incomplete final record in the local audit log.
	AuditRepaired Type = "audit.repaired"
	// An authenticated operator requested a control-plane mutation. Durable
	// admin audit records this before execution, so it is not a success claim.
	AdminMutationRequested Type = "admin.mutation_requested"
	// An allowed visitor was sent to log in again because the route's
	// max_auth_age had passed (ID-005 step-up).
	AccessStepUp       Type = "access.step_up"
	LoginStarted       Type = "login.started"
	LoginFailed        Type = "login.failed"
	LoginSucceeded     Type = "login.succeeded"
	RouteCreated       Type = "route.created"
	RouteCreateFailed  Type = "route.create_failed"
	RouteDeleted       Type = "route.deleted"
	RouteDeleteFailed  Type = "route.delete_failed"
	ConfigReloaded     Type = "config.reloaded"
	ConfigReloadFailed Type = "config.reload_failed"
	// Deployments of static sites (PAGES-002). Host carries the site name.
	PagesDeployed      Type = "pages.deployed"
	PagesRolledBack    Type = "pages.rolled_back"
	PagesRefused       Type = "pages.refused"
	PagesFailed        Type = "pages.failed"
	PagesDeployRefused Type = "pages.deploy_refused"
	// The console created a hostname's DNS record through the configured
	// provider (UI-006). Host is the hostname; Code is the record type.
	DNSRecordCreated Type = "dns.record_created"
	// A user enrolled a passkey (ID-003). Code is "synced" or "device_bound".
	PasskeyRegistered Type = "passkey.registered"
	// A passkey was revoked, by its user from a session (ID-008) or by the
	// operator; Reason says which.
	PasskeyRevoked Type = "passkey.revoked"
	// Operator changes to people from the console. Host carries the user's
	// email; an invitation's token is never part of an event.
	UserGroupsChanged Type = "user.groups_changed"
	UserInvited       Type = "user.invited"
	UserDisabled      Type = "user.disabled"
	UserEnabled       Type = "user.enabled"
	// Operator changes to the tunnel connector registry (CLI-001). Host
	// carries the connector name, Reason the certificate's short SHA-256.
	ConnectorEnrolled Type = "connector.enrolled"
	ConnectorRotated  Type = "connector.rotated"
	ConnectorRevoked  Type = "connector.revoked"
	// A Telegram chat was paired to receive alerts (NTF-001, CLI-001). Code
	// carries "chat:<id>"; the pairing code itself is never part of an event.
	NotifyChatPaired Type = "notify.chat_paired"
)

// Event is one decision or change with the context needed to explain it.
type Event struct {
	Time       time.Time
	Type       Type
	Actor      string // principal subject, or "admin" / "signal:SIGHUP"
	Host       string
	Code       string // stable decision code, e.g. auth.Decision.Code
	Reason     string
	Error      string // in-process diagnostic only; production sinks redact it
	RemoteAddr string
	// Count, when > 0, summarises repeated identical events that were
	// suppressed inside an aggregation window (see originauth denials).
	Count int
	// ConfigSHA256 is set on config.reloaded or config.reload_failed: the
	// digest of the config file in effect afterwards (the previous one when
	// the reload failed).
	ConfigSHA256 string
	// Context is attached to access decision events as evidence, not as a
	// second policy input or risk score.
	Context    *policycontext.Context `json:"context,omitempty"`
	Deployment *DeploymentSource      `json:"deployment,omitempty"`
}

// DeploymentSource is reviewed provenance, never credentials or a raw error.
type DeploymentSource struct {
	Project      string `json:"project"`
	RepositoryID int64  `json:"repository_id,omitempty"`
	Ref          string `json:"ref,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
}

// safeForObservers clears arbitrary error strings before they enter live
// activity, structured logs or an optional sink. Producers should use Code and
// Reason for stable, reviewed diagnostics instead.
func safeForObservers(e Event) Event {
	e.Error = ""
	return e
}

// Sink receives events. Emit must not block: callers are on the request path
// or inside the control plane.
type Sink interface {
	Emit(Event)
}

// AuditAppender synchronously persists a security event. It is separate from
// Sink because append failure can change whether a protected request proceeds.
type AuditAppender interface {
	Append(context.Context, Event) error
}

// Discard drops every event; it is the neutral default.
var Discard Sink = discardSink{}

type discardSink struct{}

func (discardSink) Emit(Event) {}

// SlogSink writes each event as one JSON record through log/slog and omits
// arbitrary error strings.
type SlogSink struct {
	Logger *slog.Logger
}

func (s SlogSink) Emit(e Event) {
	if s.Logger == nil {
		return
	}
	attrs := make([]slog.Attr, 0, 8)
	for _, field := range []struct{ key, value string }{
		{"actor", e.Actor},
		{"host", e.Host},
		{"code", e.Code},
		{"reason", e.Reason},
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
	if e.Context != nil {
		attrs = append(attrs, slog.Any("context", e.Context))
	}
	if e.Deployment != nil {
		attrs = append(attrs, slog.Any("deployment", e.Deployment))
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
