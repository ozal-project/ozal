// Package enrolcode decides the device-code enrolment of an administrator: the
// host prints a short code, and the person types it on the login host from any
// device, where the rest happens: the page shows which server asked for the
// code, who ran the command and from where, the person says who they are and
// creates the passkey.
//
// The book is the functional core of that flow. It owns no I/O: time and
// randomness are injected, and every answer to a browser is a Decision with a
// stable code. The admin package stores the enrolment token, runs the
// WebAuthn ceremony and serves the pages.
//
// Security properties (each has a regression test):
//   - only the host can issue a code (the admin socket), and the code is shown
//     only in that terminal;
//   - a code has 40 bits of entropy, is claimed by one browser at most once and
//     only within ClaimWindow of being issued; the whole enrolment ends within
//     Lifetime;
//   - wrong codes are limited per address and in total. An address that
//     guesses too often is refused for the rest of the window, and too many
//     wrong codes overall pause new claims for a while; neither destroys a
//     code, so a guesser cannot end someone else's enrolment; while the total
//     limit is reached claims are paused, which delays a code's owner too
//     (the terminal says so and the link remains). Fewer than 2*MaxFailures
//     guesses fit in a code's ClaimWindow: below one chance in ten billion
//     per code;
//   - storing the passkey reserves the enrolment first, so a withdrawal or a
//     cancellation either happens before the store (nothing is created) or is
//     refused (the store wins); never both;
//   - the browser that claimed can withdraw the code ("this was not me"), and
//     the terminal sees which browser claimed, how many wrong codes were tried
//     and who was enrolled;
//   - the code is never looked up by anything but its hash, and the browser
//     holds a separate claim token that the terminal never sees.
//
// The first browser to type a valid code wins: there is no second factor on
// the host. That is the device-code model; the window, the limits and the
// terminal's report are what bound it.
package enrolcode

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	// ClaimWindow is how long a printed code can be typed in.
	ClaimWindow = 5 * time.Minute
	// Lifetime bounds the whole enrolment: the passkey ceremony must finish
	// within it.
	Lifetime = 15 * time.Minute
	// MaxAddressFailures wrong codes from one address refuse that address
	// until the window has passed since its first failure.
	MaxAddressFailures = 5
	// MaxFailures wrong codes in total within FailureWindow pause every new
	// claim until the window has passed since the first of them.
	MaxFailures = 30
	// FailureWindow is how long a wrong code counts.
	FailureWindow = ClaimWindow
	// MaxPending bounds how many codes can wait at once.
	MaxPending = 8

	// Alphabet has 32 symbols without I, O, 0 and 1, which are easy to
	// confuse when read aloud or copied from a terminal.
	Alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// Length symbols of Alphabet give 40 bits.
	Length = 8
)

// State is where an enrolment stands.
type State string

const (
	Pending   State = "pending"   // printed, waiting for a browser
	Claimed   State = "claimed"   // a browser typed the code and may create the passkey
	Saving    State = "saving"    // the passkey is being stored; nothing can cancel it now
	Withdrawn State = "withdrawn" // the browser said it was not them
	Replaced  State = "replaced"  // a newer code or link for the same account made this one useless
	Done      State = "done"      // the passkey was stored
	Expired   State = "expired"   // a deadline passed
)

// Decision codes a browser or the host can receive.
const (
	CodeClaimed      = "claimed"
	CodeMalformed    = "code_malformed"
	CodeUnknown      = "code_unknown"
	CodeTaken        = "code_taken"
	CodeExpired      = "code_expired"
	CodeAddressLimit = "address_limited"
	CodePaused       = "paused"
	CodeReady        = "ready"
	CodeSaving       = "saving"
	CodeWithdrawn    = "withdrawn"
	CodeEnded        = "ended"
	CodeClaimUnknown = "claim_unknown"
)

// Decision is the outcome of one step, with a stable code and a reason for
// people.
type Decision struct {
	Allowed bool
	Code    string
	Reason  string
}

func allow(code, reason string) Decision { return Decision{Allowed: true, Code: code, Reason: reason} }
func deny(code, reason string) Decision  { return Decision{Code: code, Reason: reason} }

// Request is what the host asks to enrol. Kind and, for a recovery, Email say
// what the passkey grants. Server, Operator and From describe the request so
// the person recognises their own: the host's name, who ran the command and
// the address their SSH session came from (empty on a local terminal). Token
// is the one-use enrolment token the admin package stored; the book hands it
// only to the claiming browser's ceremony.
type Request struct {
	Kind     string
	Email    string
	Server   string
	Operator string
	From     string
	Token    string
}

// Issued is what the terminal receives: a secret to follow the enrolment and
// the code to show.
type Issued struct {
	Poll    string
	Code    string // display form, e.g. "ABCD-EFGH"
	ClaimBy time.Time
	Expires time.Time
}

// Browser describes who typed the code.
type Browser struct {
	Label   string // e.g. "Chrome on macOS"
	Address string // the visitor's IP as the edge saw it
	Country string // two-letter code from a local table, or "" (cosmetic only)
}

// View is an enrolment as the terminal or the browser may see it. It never
// contains the code or the browser's claim token.
type View struct {
	State    State
	Kind     string
	Email    string
	Server   string
	Operator string
	From     string
	// Account is who the stored passkey belongs to, once Done.
	Account string
	Browser Browser
	// Failures is how many wrong codes were typed while this one waited.
	Failures int
	// Paused is true while too many wrong codes pause every claim.
	Paused  bool
	Issued  time.Time
	Claimed time.Time
	ClaimBy time.Time
	Expires time.Time
}

type grant struct {
	poll     string
	codeHash [32]byte
	claim    [32]byte // hash of the browser's claim token; zero until claimed
	request  Request
	state    State
	browser  Browser
	account  string
	failures int
	issued   time.Time
	claimed  time.Time
}

// Book holds pending enrolments in memory; a restart ends them.
type Book struct {
	mu     sync.Mutex
	now    func() time.Time
	random io.Reader
	grants map[string]*grant
	// failures are the times of recent wrong codes, overall and per address.
	failures []time.Time
	byAddr   map[string][]time.Time
}

// New returns an empty book. now and random are injected for tests; random
// must be a cryptographically secure source in production.
func New(now func() time.Time, random io.Reader) *Book {
	return &Book{now: now, random: random, grants: map[string]*grant{}, byAddr: map[string][]time.Time{}}
}

// ErrFull means MaxPending codes are already waiting.
var ErrFull = errors.New("too many enrolment codes are waiting; let one finish or expire")

// Issue creates an enrolment and its code. Call it before changing anything
// else: when the book is full nothing has been touched.
func (b *Book) Issue(req Request) (Issued, error) {
	code, err := newCode(b.random)
	if err != nil {
		return Issued{}, err
	}
	poll, err := newSecret(b.random)
	if err != nil {
		return Issued{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.sweep(now)
	if b.live() >= MaxPending {
		return Issued{}, ErrFull
	}
	b.grants[poll] = &grant{poll: poll, codeHash: hashCode(code), request: req, state: Pending, issued: now}
	return Issued{Poll: poll, Code: Display(code), ClaimBy: now.Add(ClaimWindow), Expires: now.Add(Lifetime)}, nil
}

// Live reports whether any enrolment is waiting for a browser or a ceremony.
// The login host serves the code page only then.
func (b *Book) Live() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweep(b.now())
	return b.live() > 0
}

// Claim is a browser typing a code. On success it returns the claim token the
// browser keeps for the next steps.
func (b *Book) Claim(typed string, who Browser) (Decision, string, View) {
	code, ok := Normalize(typed)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.sweep(now)
	if len(b.byAddr[who.Address]) >= MaxAddressFailures {
		return deny(CodeAddressLimit, "too many wrong codes from this network; wait a few minutes or run the command on the server again"), "", View{}
	}
	if len(b.failures) >= MaxFailures {
		return deny(CodePaused, "too many wrong codes were tried on this server; wait a few minutes and try again"), "", View{}
	}
	if !ok {
		b.fail(now, who.Address)
		return deny(CodeMalformed, "a code has 8 letters and digits, like ABCD-EFGH"), "", View{}
	}
	want := hashCode(code)
	var g *grant
	for _, candidate := range b.grants {
		if subtle.ConstantTimeCompare(candidate.codeHash[:], want[:]) == 1 {
			g = candidate
		}
	}
	if g == nil {
		b.fail(now, who.Address)
		return deny(CodeUnknown, "this code is not valid; check it, or run the command on the server again"), "", View{}
	}
	switch g.state {
	case Pending:
	case Expired:
		return deny(CodeExpired, "this code expired; run the command on the server again"), "", View{}
	case Replaced:
		return deny(CodeEnded, "a newer code replaced this one; use the latest code the server printed"), "", View{}
	default:
		return deny(CodeTaken, "this code was already used; run the command on the server again"), "", View{}
	}
	token, err := newSecret(b.random)
	if err != nil {
		return deny(CodeUnknown, "the code could not be accepted; try again"), "", View{}
	}
	g.claim = sha256.Sum256([]byte(token))
	g.state, g.browser, g.claimed = Claimed, who, now
	return allow(CodeClaimed, ""), token, b.view(g)
}

// Watch is the terminal following its enrolment.
func (b *Book) Watch(poll string) (View, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweep(b.now())
	g, ok := b.grants[poll]
	if !ok {
		return View{}, false
	}
	return b.view(g), true
}

// Ceremony returns the request a passkey may be created for: only for the
// claim token of the browser that typed the code, while it is still claimed.
func (b *Book) Ceremony(claim string) (Decision, Request, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweep(b.now())
	g := b.byClaim(claim)
	if g == nil {
		return deny(CodeClaimUnknown, "this enrolment ended; run the command on the server again"), Request{}, ""
	}
	if g.state != Claimed {
		return deny(CodeEnded, "this enrolment ended; run the command on the server again"), Request{}, ""
	}
	return allow(CodeReady, ""), g.request, g.poll
}

// Reserve moves a claimed enrolment to Saving before its passkey is stored.
// From then on Withdraw and Cancel are refused, so a cancellation can never
// report success for a passkey that is stored anyway. Finish or Release must
// follow.
func (b *Book) Reserve(claim string) (Decision, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweep(b.now())
	g := b.byClaim(claim)
	if g == nil {
		return deny(CodeClaimUnknown, "this enrolment ended; run the command on the server again"), ""
	}
	if g.state != Claimed {
		return deny(CodeEnded, "this enrolment ended; run the command on the server again"), ""
	}
	g.state = Saving
	return allow(CodeSaving, ""), g.poll
}

// Release returns a reserved enrolment to Claimed when storing failed, so the
// browser can try again.
func (b *Book) Release(poll string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if g, ok := b.grants[poll]; ok && g.state == Saving {
		g.state = Claimed
	}
}

// Finish records that the passkey of account was stored. The terminal sees
// Done and who was enrolled.
func (b *Book) Finish(poll, account string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if g, ok := b.grants[poll]; ok && g.state == Saving {
		g.state, g.account = Done, account
	}
}

// Supersede ends every other live enrolment of the same kind and email:
// a newer recovery retires the earlier one's token, and once the first
// administrator exists other first-run codes can no longer succeed. Their
// terminals see Replaced instead of waiting for nothing.
func (b *Book) Supersede(except, kind, email string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for poll, g := range b.grants {
		if poll != except && g.request.Kind == kind && g.request.Email == email && (g.state == Pending || g.state == Claimed) {
			g.state = Replaced
		}
	}
}

// Withdraw is the claiming browser saying the request was not theirs; the
// code then cannot be used by anyone. It is refused once the passkey is being
// stored.
func (b *Book) Withdraw(claim string) Decision {
	b.mu.Lock()
	defer b.mu.Unlock()
	g := b.byClaim(claim)
	switch {
	case g == nil:
		return deny(CodeClaimUnknown, "this enrolment already ended")
	case g.state == Saving || g.state == Done:
		return deny(CodeSaving, "the passkey is already being saved, so the code can no longer be cancelled")
	case g.state != Claimed:
		return deny(CodeEnded, "this enrolment already ended")
	}
	g.state = Withdrawn
	return allow(CodeWithdrawn, "")
}

// Cancel ends an enrolment from the host, for example when the terminal is
// interrupted. Like Withdraw, it is refused once the passkey is being stored.
func (b *Book) Cancel(poll string) Decision {
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.grants[poll]
	if !ok {
		return allow(CodeEnded, "")
	}
	if g.state == Saving || g.state == Done {
		return deny(CodeSaving, "the passkey is already being saved")
	}
	delete(b.grants, poll)
	return allow(CodeWithdrawn, "")
}

func (b *Book) byClaim(claim string) *grant {
	if claim == "" || len(claim) > 64 {
		return nil
	}
	want := sha256.Sum256([]byte(claim))
	var found *grant
	for _, g := range b.grants {
		if g.state != Pending && subtle.ConstantTimeCompare(g.claim[:], want[:]) == 1 {
			found = g
		}
	}
	return found
}

// fail counts a wrong code against its address and in total, and tells every
// waiting enrolment, so the terminal can report it.
func (b *Book) fail(now time.Time, address string) {
	b.failures = append(b.failures, now)
	b.byAddr[address] = append(b.byAddr[address], now)
	for _, g := range b.grants {
		if g.state == Pending {
			g.failures++
		}
	}
}

// sweep marks deadlines, forgets old wrong codes and forgets finished
// enrolments a while later, so the terminal can still read the final state.
func (b *Book) sweep(now time.Time) {
	for poll, g := range b.grants {
		switch {
		case !now.Before(g.issued.Add(Lifetime + time.Minute)):
			delete(b.grants, poll)
		case g.state == Pending && !now.Before(g.issued.Add(ClaimWindow)):
			g.state = Expired
		case g.state == Claimed && !now.Before(g.issued.Add(Lifetime)):
			g.state = Expired
		}
	}
	b.failures = recent(b.failures, now)
	for addr, times := range b.byAddr {
		if times = recent(times, now); len(times) == 0 {
			delete(b.byAddr, addr)
		} else {
			b.byAddr[addr] = times
		}
	}
}

// recent keeps the failures still inside FailureWindow.
func recent(times []time.Time, now time.Time) []time.Time {
	kept := times[:0]
	for _, t := range times {
		if now.Before(t.Add(FailureWindow)) {
			kept = append(kept, t)
		}
	}
	return kept
}

func (b *Book) live() int {
	n := 0
	for _, g := range b.grants {
		if g.state == Pending || g.state == Claimed || g.state == Saving {
			n++
		}
	}
	return n
}

func (b *Book) view(g *grant) View {
	r := g.request
	return View{
		State: g.state, Kind: r.Kind, Email: r.Email, Server: r.Server, Operator: r.Operator, From: r.From,
		Account: g.account, Browser: g.browser, Failures: g.failures, Paused: len(b.failures) >= MaxFailures,
		Issued: g.issued, Claimed: g.claimed,
		ClaimBy: g.issued.Add(ClaimWindow), Expires: g.issued.Add(Lifetime),
	}
}

// Normalize turns what a person typed into the canonical code: case and the
// separators people add (spaces, hyphens) do not matter.
func Normalize(typed string) (string, bool) {
	if len(typed) > 32 {
		return "", false
	}
	var out strings.Builder
	for _, r := range strings.ToUpper(typed) {
		switch {
		case r == ' ' || r == '-' || r == '\t':
			continue
		case strings.ContainsRune(Alphabet, r):
			out.WriteRune(r)
		default:
			return "", false
		}
	}
	if out.Len() != Length {
		return "", false
	}
	return out.String(), true
}

// Display groups a code in two halves.
func Display(code string) string {
	if len(code) != Length {
		return code
	}
	return code[:Length/2] + "-" + code[Length/2:]
}

func hashCode(code string) [32]byte { return sha256.Sum256([]byte("enrolcode:" + code)) }

// newCode draws Length symbols without modulo bias: 32 divides 256.
func newCode(random io.Reader) (string, error) {
	raw := make([]byte, Length)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	out := make([]byte, Length)
	for i, v := range raw {
		out[i] = Alphabet[int(v)%len(Alphabet)]
	}
	return string(out), nil
}

func newSecret(random io.Reader) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
