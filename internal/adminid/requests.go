package adminid

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"sync"
	"time"
)

// Access requests: a person who has no account asks to become an
// administrator and a signed-in administrator decides. A request grants
// nothing by itself. It lives in memory only, so a restart ends it, and it
// carries no secret that logs anyone in: the requester holds a random secret
// that only they can use to collect the enrolment link an approval produces.
// That binding is what keeps an approver from handing the link to someone
// other than the browser that asked.

var (
	// ErrRequestBusy means too many requests are waiting or arrived too fast.
	ErrRequestBusy = errors.New("admin: too many access requests; try again later")
	// ErrRequestGone means the request is unknown, decided or expired.
	ErrRequestGone = errors.New("admin: access request not found or expired")
)

const (
	// RequestTTL is how long a request waits for a decision.
	RequestTTL = 24 * time.Hour
	// ApprovedTTL is how long an approved requester has to collect the link;
	// it matches the enrolment token's own life.
	ApprovedTTL = 15 * time.Minute

	maxPendingRequests = 32
	submitWindow       = 10 * time.Minute
	maxSubmitsInWindow = 10
)

// RequestState is what a requester sees when they ask how it went.
type RequestState string

const (
	RequestPending  RequestState = "pending"
	RequestApproved RequestState = "approved"
	RequestDenied   RequestState = "denied"
)

// Request is one waiting access request, as an administrator reviews it.
type Request struct {
	ID      string
	Name    string
	Email   string
	Label   string
	Created time.Time
	Expires time.Time
}

type entry struct {
	Request
	secret [32]byte
	state  RequestState
	// hidden entries answer the requester like any other but are never
	// listed or approved: they stand in for a request whose email already is
	// an administrator, so the public form does not reveal who is one.
	hidden bool
	token  string
}

// Requests is the in-memory book of access requests.
type Requests struct {
	mu      sync.Mutex
	now     func() time.Time
	random  io.Reader
	items   map[string]*entry
	submits []time.Time
}

// NewRequests builds an empty book on the real clock and randomness.
func NewRequests() *Requests {
	return &Requests{now: time.Now, random: rand.Reader, items: map[string]*entry{}}
}

// NewRequestsAt builds a book with an injected clock and randomness, for tests.
func NewRequestsAt(now func() time.Time, random io.Reader) *Requests {
	return &Requests{now: now, random: random, items: map[string]*entry{}}
}

func (b *Requests) expire(now time.Time) {
	for id, e := range b.items {
		if !now.Before(e.Expires) {
			delete(b.items, id)
		}
	}
	keep := b.submits[:0]
	for _, t := range b.submits {
		if now.Sub(t) < submitWindow {
			keep = append(keep, t)
		}
	}
	b.submits = keep
}

// Submit records a request and returns the ID the requester polls with and
// the secret only they hold. A second request from the same email replaces the
// first. hidden marks a request that must look accepted but never reaches an
// administrator.
func (b *Requests) Submit(name, email, label string, hidden bool) (id, secret string, expires time.Time, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.expire(now)
	if len(b.submits) >= maxSubmitsInWindow {
		return "", "", time.Time{}, ErrRequestBusy
	}
	for key, e := range b.items {
		if e.Email == email && e.state == RequestPending {
			delete(b.items, key)
		}
	}
	if len(b.items) >= maxPendingRequests {
		return "", "", time.Time{}, ErrRequestBusy
	}
	idRaw, secretRaw := make([]byte, 8), make([]byte, 32)
	if _, err := io.ReadFull(b.random, idRaw); err != nil {
		return "", "", time.Time{}, err
	}
	if _, err := io.ReadFull(b.random, secretRaw); err != nil {
		return "", "", time.Time{}, err
	}
	id = hex.EncodeToString(idRaw)
	secret = base64.RawURLEncoding.EncodeToString(secretRaw)
	expires = now.Add(RequestTTL)
	b.items[id] = &entry{
		Request: Request{ID: id, Name: name, Email: email, Label: label, Created: now, Expires: expires},
		secret:  sha256.Sum256([]byte(secret)),
		state:   RequestPending,
		hidden:  hidden,
	}
	b.submits = append(b.submits, now)
	return id, secret, expires, nil
}

// Pending lists the requests waiting for a decision, oldest first.
func (b *Requests) Pending() []Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expire(b.now())
	out := []Request{}
	for _, e := range b.items {
		if e.state == RequestPending && !e.hidden {
			out = append(out, e.Request)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Get returns one waiting, reviewable request.
func (b *Requests) Get(id string) (Request, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expire(b.now())
	e, ok := b.items[id]
	if !ok || e.state != RequestPending || e.hidden {
		return Request{}, ErrRequestGone
	}
	return e.Request, nil
}

// Approve records the enrolment token an approval produced. The token is held
// only until the requester collects it, and no longer than ApprovedTTL.
func (b *Requests) Approve(id, token string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.expire(now)
	e, ok := b.items[id]
	if !ok || e.state != RequestPending || e.hidden {
		return ErrRequestGone
	}
	e.state, e.token = RequestApproved, token
	e.Expires = now.Add(ApprovedTTL)
	return nil
}

// Deny rejects a waiting request; the requester learns it once.
func (b *Requests) Deny(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.expire(now)
	e, ok := b.items[id]
	if !ok || e.state != RequestPending || e.hidden {
		return ErrRequestGone
	}
	e.state = RequestDenied
	e.Expires = now.Add(ApprovedTTL)
	return nil
}

// Clear drops every request, for when requests are turned off.
func (b *Requests) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	clear(b.items)
}

// Poll answers the requester. A wrong secret looks exactly like an unknown
// request. An approval or a denial is delivered once and then forgotten.
func (b *Requests) Poll(id, secret string) (RequestState, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expire(b.now())
	e, ok := b.items[id]
	hash := sha256.Sum256([]byte(secret))
	if !ok || subtle.ConstantTimeCompare(hash[:], e.secret[:]) != 1 {
		return "", "", ErrRequestGone
	}
	switch e.state {
	case RequestApproved:
		token := e.token
		delete(b.items, id)
		return RequestApproved, token, nil
	case RequestDenied:
		delete(b.items, id)
		return RequestDenied, "", nil
	}
	return RequestPending, "", nil
}
