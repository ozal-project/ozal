package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
	Time   string `json:"time"`
	Type   string `json:"type"`
	Actor  string `json:"actor,omitempty"`
	Host   string `json:"host,omitempty"`
	Code   string `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
	Remote string `json:"remote,omitempty"`
}

func toView(e events.Event) eventView {
	return eventView{
		Time: e.Time.UTC().Format(time.RFC3339), Type: string(e.Type), Actor: e.Actor,
		Host: e.Host, Code: e.Code, Reason: e.Reason, Error: e.Error, Remote: e.RemoteAddr,
	}
}

// stream serves recent events and then live ones as Server-Sent Events. It
// returns when the client goes away (request context), the broker closes the
// subscription, or a write fails.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	history, live, cancel, ok := h.broker.Subscribe()
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

	send := func(e events.Event) error {
		data, err := json.Marshal(toView(e))
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
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

func (h *Handler) activityPage(w http.ResponseWriter, _ *http.Request) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; font-src 'self'; script-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	hdr.Set("X-Content-Type-Options", "nosniff")
	if err := activityTemplate.ExecuteTemplate(w, "activity.html", statusView{Active: "activity", HasActivity: true}); err != nil {
		log.Printf("render activity: %v", err)
	}
}

func (h *Handler) activityScript(w http.ResponseWriter, _ *http.Request) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/javascript; charset=utf-8")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")
	asset, _ := consoleAssets.ReadFile("ui/activity.js")
	_, _ = w.Write(asset)
}
