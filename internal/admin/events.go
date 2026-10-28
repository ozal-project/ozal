package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/policycontext"
)

// Option configures optional admin features.
type Option func(*Handler)

// WithEvents serves the live activity page backed by broker.
func WithEvents(broker *events.Broker) Option {
	return func(h *Handler) { h.broker = broker }
}

// WithCertificateStatus adds the origin-certificate panel. A nil provider is
// ignored, so the autocert mode simply omits the panel.
func WithCertificateStatus(provider func() CertificateStatus) Option {
	return func(h *Handler) {
		if provider != nil {
			h.certificate = provider
		}
	}
}

// WithAuditStatus adds the durable audit state for selected event families to
// both admin status views. A nil provider omits the field when audit is not
// configured.
func WithAuditStatus(provider func() AuditStatus) Option {
	return func(h *Handler) {
		if provider != nil {
			h.auditStatus = provider
		}
	}
}

// WithAdminMutationAudit installs the durable intent appender for admin
// mutations. Required mode fails the request before the mutation adapter runs.
func WithAdminMutationAudit(appender events.AuditAppender, required bool) Option {
	return func(h *Handler) {
		h.mutationAudit = appender
		h.auditMutationsRequired = required
	}
}

func (h *Handler) recordMutationIntent(w http.ResponseWriter, r *http.Request, code, target string) bool {
	if h.mutationAudit == nil {
		if h.auditMutationsRequired {
			http.Error(w, "administrative audit unavailable", http.StatusServiceUnavailable)
			return false
		}
		return true
	}
	event := events.Event{
		Type: events.AdminMutationRequested, Actor: actor(r), Host: safeMutationTarget(target),
		Code: code, Reason: "authenticated operator requested mutation",
	}
	appendErr := h.mutationAudit.Append(r.Context(), event)
	if appendErr != nil && h.auditMutationsRequired {
		http.Error(w, "administrative audit unavailable", http.StatusServiceUnavailable)
		return false
	}
	h.emit(event)
	return true
}

func safeMutationTarget(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 254 {
		return ""
	}
	for _, char := range value {
		if unicode.IsControl(char) || unicode.IsSpace(char) || char == '/' || char == '\\' {
			return ""
		}
	}
	return value
}

const heartbeat = 15 * time.Second

// streamAuthorized rechecks authority while a stream remains open. A revoked
// or expired operator cannot keep reading events on an existing connection.
func (h *Handler) streamAuthorized(r *http.Request) bool {
	if isLocal(r) {
		return true
	}
	_, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok {
		return false
	}
	if grant, ok := r.Context().Value(operatorKey{}).(operatorGrant); ok {
		current, valid := h.operators.authenticate(token)
		return valid && current.ID == grant.ID
	}
	adminToken, err := config.ResolveSecret(h.manager.Config().Admin.Token)
	return err == nil && adminToken != "" && matchesAny(token, []string{adminToken})
}

type eventView struct {
	ID         string                   `json:"id"`
	Time       string                   `json:"time"`
	Type       string                   `json:"type"`
	Actor      string                   `json:"actor,omitempty"`
	Host       string                   `json:"host,omitempty"`
	Code       string                   `json:"code,omitempty"`
	Reason     string                   `json:"reason,omitempty"`
	Remote     string                   `json:"remote,omitempty"`
	Context    *policycontext.Context   `json:"context,omitempty"`
	Deployment *events.DeploymentSource `json:"deployment,omitempty"`
}

func toView(id string, e events.Event) eventView {
	return eventView{
		ID: id, Time: e.Time.UTC().Format(time.RFC3339), Type: string(e.Type), Actor: e.Actor,
		Host: e.Host, Code: e.Code, Reason: e.Reason, Remote: e.RemoteAddr,
		Context: e.Context, Deployment: e.Deployment,
	}
}

// eventID is an event's identity on the wire: the broker's epoch and the
// event's sequence number in it (BUG-009).
func eventID(epoch string, seq uint64) string { return epoch + "-" + strconv.FormatUint(seq, 10) }

// resumeAfter reads the Last-Event-ID a reconnecting EventSource sends. Only
// an ID from this broker's epoch resumes; anything else (a restart, garbage,
// a first connection) gets the whole retained history.
func resumeAfter(lastID, epoch string) uint64 {
	prefix, seq, ok := strings.Cut(lastID, "-")
	if !ok || prefix != epoch {
		return 0
	}
	n, err := strconv.ParseUint(seq, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// stream serves recent events and then live ones as Server-Sent Events, each
// with an id, so a reconnecting browser sends Last-Event-ID and receives only
// what came after it. It returns when the client goes away (request
// context), the broker closes the subscription, or a write fails.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	epoch := h.broker.Epoch()
	history, live, cancel, ok := h.broker.Subscribe(resumeAfter(r.Header.Get("Last-Event-ID"), epoch))
	if !ok {
		http.Error(w, "too many event streams", http.StatusServiceUnavailable)
		return
	}
	defer cancel()

	// The server-wide WriteTimeout would cut a long-lived stream; lift it for
	// this request only.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")

	send := func(e events.Stamped) error {
		if !h.streamAuthorized(r) {
			return errors.New("event stream authority expired or revoked")
		}
		id := eventID(epoch, e.Seq)
		data, err := json.Marshal(toView(id, e.Event))
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", id, data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	for _, e := range history {
		if send(e) != nil {
			return
		}
	}
	flusher.Flush()

	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, open := <-live:
			if !open || send(e) != nil {
				return
			}
		case <-tick.C:
			if !h.streamAuthorized(r) {
				return
			}
			// A comment line keeps proxies and the browser from timing out
			// and surfaces a dead client as a write error.
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
