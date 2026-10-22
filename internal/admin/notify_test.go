package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/notify"
)

// fakePairing pairs chat 7 as soon as it starts, unless told to wait.
type fakePairing struct {
	wait    bool
	started int
	status  notify.PairingStatus
}

func (f *fakePairing) Start(ttl time.Duration, onPaired func(int64)) (string, time.Time, error) {
	if f.status.State == notify.PairingWaiting {
		return "", time.Time{}, notify.ErrPairingActive
	}
	f.started++
	f.status = notify.PairingStatus{State: notify.PairingWaiting, Expires: time.Now().Add(ttl)}
	if !f.wait {
		f.status.State, f.status.ChatID = notify.PairingPaired, 7
		onPaired(7)
	}
	return "ABCDEFGH", f.status.Expires, nil
}

func (f *fakePairing) Status() notify.PairingStatus { return f.status }

func (f *fakePairing) Cancel() bool {
	if f.status.State != notify.PairingWaiting {
		return false
	}
	f.status.State = notify.PairingCancelled
	return true
}

func pairingHandler(p pairing) (http.Handler, *events.Recorder) {
	recorder := &events.Recorder{}
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	return New(manager, recorder, WithPairing(p)), recorder
}

// A paired chat receives every alert: no bearer may start a pairing, read
// its state or cancel it.
func TestPairingIsHostOnly(t *testing.T) {
	p := &fakePairing{}
	handler, recorder := pairingHandler(p)
	for _, c := range []struct{ method, body string }{
		{http.MethodPost, `{"timeout_seconds": 60}`},
		{http.MethodGet, ""},
		{http.MethodDelete, ""},
	} {
		if rec := request(handler, c.method, "/_/api/notify/pair", "secret", c.body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s with the admin token = %d, want 403", c.method, rec.Code)
		}
	}
	if p.started != 0 || len(recorder.Events()) != 0 {
		t.Fatalf("refused requests started %d pairings and emitted %+v", p.started, recorder.Events())
	}

	rec := local(handler, http.MethodPost, "/_/api/notify/pair", `{"timeout_seconds": 60}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"code":"ABCDEFGH"`) {
		t.Fatalf("start on the socket = %d %s", rec.Code, rec.Body)
	}
	got := recorder.Events()
	if len(got) != 1 || got[0].Type != events.NotifyChatPaired || got[0].Actor != "local" || got[0].Code != "chat:7" {
		t.Fatalf("events = %+v", got)
	}

	rec = local(handler, http.MethodGet, "/_/api/notify/pair", "")
	var view map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view["state"] != "paired" || view["chat_id"] != float64(7) || view["code"] != nil {
		t.Fatalf("status = %v, want paired chat 7 and no code", view)
	}
}

func TestPairingRequestsAreBounded(t *testing.T) {
	p := &fakePairing{wait: true}
	handler, _ := pairingHandler(p)
	for _, body := range []string{`{}`, `{"timeout_seconds": 0}`, `{"timeout_seconds": 3601}`, `{"timeout_seconds": -5}`} {
		if rec := local(handler, http.MethodPost, "/_/api/notify/pair", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("start %s = %d, want 400", body, rec.Code)
		}
	}
	if rec := local(handler, http.MethodPost, "/_/api/notify/pair", `{"timeout_seconds": 60}`); rec.Code != http.StatusCreated {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	if rec := local(handler, http.MethodPost, "/_/api/notify/pair", `{"timeout_seconds": 60}`); rec.Code != http.StatusConflict {
		t.Fatalf("second start = %d, want 409", rec.Code)
	}
	if rec := local(handler, http.MethodDelete, "/_/api/notify/pair", ""); rec.Code != http.StatusOK {
		t.Fatalf("cancel = %d", rec.Code)
	}
	if rec := local(handler, http.MethodDelete, "/_/api/notify/pair", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("second cancel = %d, want 404", rec.Code)
	}
}

func TestNoPairingWithoutTelegram(t *testing.T) {
	handler, _ := connectorHandler(t)
	if rec := local(handler, http.MethodPost, "/_/api/notify/pair", `{"timeout_seconds": 60}`); rec.Code != http.StatusNotFound {
		t.Fatalf("start without a pairing = %d, want 404", rec.Code)
	}
}
