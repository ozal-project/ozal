package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/events"
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

const heartbeat = 15 * time.Second

type eventView struct {
	ID     string `json:"id"`
	Time   string `json:"time"`
	Type   string `json:"type"`
	Actor  string `json:"actor,omitempty"`
	Host   string `json:"host,omitempty"`
	Code   string `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
	Remote string `json:"remote,omitempty"`
}

func toView(id string, e events.Event) eventView {
	return eventView{
		ID: id, Time: e.Time.UTC().Format(time.RFC3339), Type: string(e.Type), Actor: e.Actor,
		Host: e.Host, Code: e.Code, Reason: e.Reason, Error: e.Error, Remote: e.RemoteAddr,
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
			// A comment line keeps proxies and the browser from timing out
			// and surfaces a dead client as a write error.
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
