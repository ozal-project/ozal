package auth

import (
	"context"
	"errors"
	"time"
)

var ErrSessionNotFound = errors.New("session not found")

// SessionInfo contains a non-bearer handle and metadata; never a cookie value.
type SessionInfo struct {
	ID       string    `json:"id"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"last_seen"`
	Expires  time.Time `json:"expires"`
	Method   string    `json:"method"`
	Current  bool      `json:"current"`
}

// SessionControl is an optional, consumer-declared self-service port.
// Subject always comes from the authenticated principal, never the request.
type SessionControl interface {
	ListOwned(context.Context, string) ([]SessionInfo, error)
	RevokeOwned(context.Context, string, string, bool) error
}
