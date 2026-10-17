package admin

import (
	"encoding/json"
	"fmt"
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
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	hdr.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(activityHTML))
}

func (h *Handler) activityScript(w http.ResponseWriter, _ *http.Request) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/javascript; charset=utf-8")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(activityJS))
}

const activityHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Switchboard Activity</title>
<style>
:root { color-scheme: light dark; --ink:#19211d; --muted:#65716b; --line:#d9dedb; --paper:#f7f8f6; --red:#a63a2b; --green:#1f7a4d; }
@media (prefers-color-scheme: dark) { :root { --ink:#e8ece9; --muted:#9aa6a0; --line:#2c3531; --paper:#141917; --red:#e0735f; --green:#5cc08d; } }
body { margin:0; background:var(--paper); color:var(--ink); font:14px/1.5 ui-sans-serif,system-ui,sans-serif; }
main { width:min(1180px,calc(100% - 32px)); margin:0 auto; padding:20px 0; }
h1 { font-size:18px; margin:0 0 4px; } p { color:var(--muted); margin:0 0 16px; }
a { color:inherit; }
table { width:100%; border-collapse:collapse; } th,td { text-align:left; padding:6px 8px; border-bottom:1px solid var(--line); vertical-align:top; }
th { color:var(--muted); font-weight:600; } td.t { white-space:nowrap; font-variant-numeric:tabular-nums; }
.deny,.fail { color:var(--red); } .allow,.ok { color:var(--green); }
#state { font-weight:600; }
</style>
</head>
<body>
<main>
<h1>Activity</h1>
<p>Access decisions, logins and control-plane changes. <span id="state">connecting…</span> · <a href="/_/status">route status</a></p>
<table><thead><tr><th>Time (UTC)</th><th>Event</th><th>Actor</th><th>Host</th><th>Detail</th></tr></thead><tbody id="rows"></tbody></table>
<noscript><p>This page needs JavaScript for the live stream.</p></noscript>
</main>
<script src="/_/activity.js"></script>
</body>
</html>`

// The script builds rows with textContent only: event fields can carry
// attacker-influenced strings (hosts, reasons), so nothing is parsed as HTML.
const activityJS = `(() => {
  const rows = document.getElementById("rows");
  const state = document.getElementById("state");
  const MAX = 500;
  const cls = (t) => t.endsWith(".denied") || t.endsWith(".failed") ? "deny" : t.endsWith(".allowed") || t.endsWith(".succeeded") ? "allow" : "";
  const cell = (tr, text, c) => { const td = document.createElement("td"); td.textContent = text || ""; if (c) td.className = c; tr.appendChild(td); };
  const es = new EventSource("/_/events");
  es.onopen = () => { state.textContent = "live"; };
  es.onerror = () => { state.textContent = "reconnecting…"; };
  es.onmessage = (m) => {
    const e = JSON.parse(m.data);
    const tr = document.createElement("tr");
    cell(tr, e.time, "t");
    cell(tr, e.type, cls(e.type));
    cell(tr, e.actor);
    cell(tr, e.host);
    cell(tr, [e.code, e.reason, e.error, e.remote].filter(Boolean).join(" · "));
    rows.prepend(tr);
    while (rows.children.length > MAX) rows.lastChild.remove();
  };
})();`
