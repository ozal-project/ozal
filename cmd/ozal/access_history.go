package main

import (
	"sync/atomic"

	"github.com/ozal-project/ozal/internal/accesshistory"
	"github.com/ozal-project/ozal/internal/audit"
	"github.com/ozal-project/ozal/internal/events"
)

// liveAccess feeds the in-memory access history from the event stream until
// the durable journal takes over (UI-005). Once off, the journal is the only
// source, so a decision is never counted twice.
type liveAccess struct {
	store *accesshistory.Store
	off   atomic.Bool
}

func (l *liveAccess) Emit(e events.Event) {
	if !l.off.Load() {
		l.store.Emit(e)
	}
}

// observeAccess turns each journal record, existing or newly committed, into
// the event the history counts. Only fields the journal keeps are carried.
func observeAccess(store *accesshistory.Store) func(audit.Record) {
	return func(r audit.Record) {
		store.Add(events.Event{
			Type: r.Type, Time: r.Time, Actor: r.Actor, Host: r.Host,
			Code: r.Code, Reason: r.Reason, RemoteAddr: r.RemoteAddr,
		})
	}
}
