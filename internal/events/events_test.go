package events

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestSlogSinkEmitsOneJSONRecordPerEvent(t *testing.T) {
	var buf bytes.Buffer
	sink := SlogSink{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}

	sink.Emit(Event{Type: AccessDenied, Actor: "user-1", Host: "app.localhost", Code: "domain_not_allowed", Reason: "email domain"})
	sink.Emit(Event{Type: LoginSucceeded, Actor: "user-1", Host: "app.localhost"})

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("records = %d, want 2 (%s)", len(lines), buf.String())
	}
	var first map[string]any
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("first record is not JSON: %v", err)
	}
	if first["msg"] != string(AccessDenied) || first["actor"] != "user-1" || first["code"] != "domain_not_allowed" {
		t.Fatalf("first record = %v", first)
	}
	if _, ok := first["time"]; !ok {
		t.Fatal("record has no time field")
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
