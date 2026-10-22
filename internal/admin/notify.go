package admin

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/notify"
)

// pairing is the Telegram chat pairing (notify.Pairer implements it): the
// operation of `switchboard notify pair` (CLI-001).
type pairing interface {
	Start(ttl time.Duration, onPaired func(chatID int64)) (string, time.Time, error)
	Status() notify.PairingStatus
	Cancel() bool
}

// WithPairing enables the chat pairing endpoints.
func WithPairing(p pairing) Option {
	return func(h *Handler) { h.pairing = p }
}

// maxPairing bounds how long one code may wait for its /pair message.
const maxPairing = time.Hour

// registerPairing: all on the host only. A paired chat receives every
// alert, and the code is what proves the chat belongs to the operator, so
// neither starting a pairing nor reading its state travels with a bearer.
func (h *Handler) registerPairing() {
	h.mux.HandleFunc("POST /_/api/notify/pair", h.requireLocal(h.startPairing))
	h.mux.HandleFunc("GET /_/api/notify/pair", h.requireLocal(h.pairingStatus))
	h.mux.HandleFunc("DELETE /_/api/notify/pair", h.requireLocal(h.cancelPairing))
}

type pairRequest struct {
	TimeoutSeconds int `json:"timeout_seconds"`
}

type pairingView struct {
	State   notify.PairingState `json:"state"`
	ChatID  int64               `json:"chat_id,omitempty"`
	Expires time.Time           `json:"expires,omitzero"`
	Code    string              `json:"code,omitempty"`
}

func (h *Handler) startPairing(w http.ResponseWriter, r *http.Request) {
	var req pairRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ttl := time.Duration(req.TimeoutSeconds) * time.Second
	if ttl <= 0 || ttl > maxPairing {
		writeError(w, http.StatusBadRequest, "timeout_seconds must be between 1 and 3600")
		return
	}
	who := actor(r)
	code, expires, err := h.pairing.Start(ttl, func(chatID int64) {
		h.emit(events.Event{Type: events.NotifyChatPaired, Actor: who, Code: "chat:" + strconv.FormatInt(chatID, 10)})
	})
	if errors.Is(err, notify.ErrPairingActive) {
		writeError(w, http.StatusConflict, "a pairing is already waiting: cancel it or let it expire")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the pairing could not start")
		return
	}
	writeJSON(w, http.StatusCreated, pairingView{State: notify.PairingWaiting, Expires: expires.UTC(), Code: code})
}

func (h *Handler) pairingStatus(w http.ResponseWriter, r *http.Request) {
	s := h.pairing.Status()
	writeJSON(w, http.StatusOK, pairingView{State: s.State, ChatID: s.ChatID, Expires: s.Expires.UTC()})
}

func (h *Handler) cancelPairing(w http.ResponseWriter, r *http.Request) {
	if !h.pairing.Cancel() {
		writeError(w, http.StatusNotFound, "no pairing is waiting")
		return
	}
	writeJSON(w, http.StatusOK, pairingView{State: notify.PairingCancelled})
}
