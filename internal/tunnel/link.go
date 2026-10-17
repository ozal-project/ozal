package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"sync"
	"time"
)

// LinkState is the connector side's view of its tunnel.
type LinkState string

const (
	LinkConnecting LinkState = "connecting"
	LinkConnected  LinkState = "connected"
	LinkBackoff    LinkState = "backoff"
	LinkStopped    LinkState = "stopped"
)

// LinkStatus is a snapshot for status pages and logs. LastError is one of the
// fixed strings Connect returns, never raw peer data.
type LinkStatus struct {
	State     LinkState
	Attempt   int       // consecutive failed attempts since the last stable link
	Since     time.Time // when State was entered
	NextRetry time.Time // set in LinkBackoff
	LastError string
}

// Link holds the observable state of one ConnectLoop; safe for concurrent use.
type Link struct {
	mu sync.Mutex
	st LinkStatus
}

func (l *Link) Status() LinkStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st
}

func (l *Link) set(s LinkStatus) {
	l.mu.Lock()
	l.st = s
	l.mu.Unlock()
}

// LoopOptions tunes ConnectLoop. The zero value uses DefaultBackoff and a
// 30s stability window.
type LoopOptions struct {
	Backoff Backoff
	// StableAfter: a link that stayed up at least this long resets the
	// attempt counter, so one blip after hours does not start at a long delay.
	StableAfter time.Duration
	Link        *Link
}

// ConnectLoop keeps a connector attached until ctx ends: it dials, serves, and
// on any failure waits with jittered exponential backoff before dialing again.
// It returns nil on cancellation, or an error if the TLS setup is unusable
// (retrying would never help).
func ConnectLoop(ctx context.Context, addr string, tlsConfig *tls.Config, handler http.Handler, opts LoopOptions) error {
	if err := validateClient(tlsConfig, handler); err != nil {
		return err
	}
	if opts.Backoff.Base <= 0 {
		opts.Backoff = DefaultBackoff
	}
	if opts.StableAfter <= 0 {
		opts.StableAfter = 30 * time.Second
	}
	link := opts.Link
	if link == nil {
		link = new(Link)
	}
	attempt := 0
	for {
		link.set(LinkStatus{State: LinkConnecting, Attempt: attempt, Since: time.Now()})
		var upAt time.Time
		err := connect(ctx, addr, tlsConfig, handler, func() {
			upAt = time.Now()
			link.set(LinkStatus{State: LinkConnected, Since: upAt})
		})
		if ctx.Err() != nil {
			link.set(LinkStatus{State: LinkStopped, Since: time.Now()})
			return nil
		}
		if !upAt.IsZero() && time.Since(upAt) >= opts.StableAfter {
			attempt = 0
		}
		delay := opts.Backoff.Delay(attempt)
		attempt++
		msg := "tunnel connection closed"
		if err != nil {
			msg = err.Error()
		}
		now := time.Now()
		link.set(LinkStatus{State: LinkBackoff, Attempt: attempt, Since: now, NextRetry: now.Add(delay), LastError: msg})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			link.set(LinkStatus{State: LinkStopped, Since: time.Now()})
			return nil
		case <-timer.C:
		}
	}
}

func validateClient(tlsConfig *tls.Config, handler http.Handler) error {
	if handler == nil || tlsConfig == nil || tlsConfig.InsecureSkipVerify || tlsConfig.ServerName == "" || tlsConfig.RootCAs == nil || len(tlsConfig.Certificates) == 0 {
		return errors.New("verified mutual TLS is required")
	}
	return nil
}
