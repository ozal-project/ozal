package events

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/policycontext"
)

func TestSlogSinkEmitsOneJSONRecordPerEvent(t *testing.T) {
	var buf bytes.Buffer
	sink := SlogSink{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}

	decisionContext := policycontext.Build(policycontext.Input{
		ObservedAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		Route:      policycontext.Known("app.localhost", policycontext.SourceRouteTable),
	})
	sink.Emit(Event{Type: AccessDenied, Actor: "user-1", Host: "app.localhost", Code: "domain_not_allowed", Reason: "email domain", Context: &decisionContext})
	sink.Emit(Event{Type: LoginSucceeded, Actor: "user-1", Host: "app.localhost", Count: 3, Error: "/private/path: secret-token"})
	sink.Emit(Event{Type: ConfigReloaded, Actor: "signal:SIGHUP", ConfigSHA256: "abc123"})

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("records = %d, want 3 (%s)", len(lines), buf.String())
	}
	var reload map[string]any
	if err := json.Unmarshal(lines[2], &reload); err != nil || reload["config_sha256"] != "abc123" {
		t.Fatalf("config.reloaded record does not carry config_sha256: %s", lines[2])
	}
	var first map[string]any
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("first record is not JSON: %v", err)
	}
	if first["msg"] != string(AccessDenied) || first["actor"] != "user-1" || first["code"] != "domain_not_allowed" {
		t.Fatalf("first record = %v", first)
	}
	context, ok := first["context"].(map[string]any)
	if !ok || context["version"] != float64(policycontext.Version) {
		t.Fatalf("decision context was not preserved in JSON event: %v", first["context"])
	}
	if _, ok := first["time"]; !ok {
		t.Fatal("record has no time field")
	}
	if _, ok := first["count"]; ok {
		t.Fatalf("record without suppressed events carries count: %v", first)
	}
	var second map[string]any
	if err := json.Unmarshal(lines[1], &second); err != nil {
		t.Fatalf("second record is not JSON: %v", err)
	}
	if second["count"] != float64(3) {
		t.Fatalf("second record count = %v, want 3", second["count"])
	}
	if _, ok := second["error"]; ok {
		t.Fatalf("structured event log retained the raw error field: %v", second)
	}
	if strings.Contains(buf.String(), "/private/path") || strings.Contains(buf.String(), "secret-token") {
		t.Fatalf("structured event log exposed an internal error: %s", buf.String())
	}
}

func TestMultiClearsRawErrorsBeforeForwarding(t *testing.T) {
	const raw = "/private/switchboard/state.db: secret-token"
	var recorder Recorder
	Multi{&recorder}.Emit(Event{Type: LoginFailed, Code: "session_create_failed", Error: raw})
	got := recorder.Events()
	if len(got) != 1 || got[0].Error != "" {
		t.Fatalf("sink received raw event error: %+v", got)
	}
}

func TestRecorderCapturesAndFinds(t *testing.T) {
	var rec Recorder
	rec.Emit(Event{Type: LoginStarted})
	rec.Emit(Event{Type: AccessAllowed, Host: "app.localhost"})

	if !rec.Has(LoginStarted) {
		t.Fatal("recorder lost login.started")
	}
	allowed, ok := rec.Find(AccessAllowed)
	if !ok || allowed.Host != "app.localhost" {
		t.Fatalf("Find(AccessAllowed) = %+v, %v", allowed, ok)
	}
	if rec.Has(RouteCreated) {
		t.Fatal("recorder invented route.created")
	}
}
