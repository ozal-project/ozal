package enrolcode

import (
	"crypto/rand"
	"fmt"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newBook() (*Book, *clock) {
	c := &clock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	return New(c.now, rand.Reader), c
}

var who = Browser{Label: "Chrome on macOS", Address: "203.0.113.7"}

func issue(t *testing.T, b *Book) Issued {
	t.Helper()
	issued, err := b.Issue(Request{Kind: "bootstrap", Server: "lab", Operator: "saint", From: "192.168.100.5", Token: "enrol-token"})
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func TestCodeShapeAndNormalize(t *testing.T) {
	b, _ := newBook()
	issued := issue(t, b)
	if len(issued.Code) != Length+1 || issued.Code[Length/2] != '-' {
		t.Fatalf("code %q is not two groups of four", issued.Code)
	}
	code, ok := Normalize(issued.Code)
	if !ok {
		t.Fatalf("issued code %q does not normalize", issued.Code)
	}
	for _, typed := range []string{strings.ToLower(issued.Code), " " + code[:4] + " " + code[4:] + " ", code} {
		if got, ok := Normalize(typed); !ok || got != code {
			t.Errorf("Normalize(%q) = %q, %v", typed, got, ok)
		}
	}
	for _, typed := range []string{"", "ABCD-EFG", "ABCD-EFGHJ", "ABCD-EFG0", "ABCD-EFGI", "ABCD/EFGH", strings.Repeat("A", 40)} {
		if _, ok := Normalize(typed); ok {
			t.Errorf("Normalize(%q) accepted", typed)
		}
	}
}

// The browser that types the code may create the passkey; no other browser
// can, the code works once, and the terminal sees who claimed and who was
// enrolled. The claim tells the browser whose request this is.
func TestTheClaimingBrowserCreatesThePasskey(t *testing.T) {
	b, _ := newBook()
	issued := issue(t, b)
	if !b.Live() {
		t.Fatal("a printed code is not live")
	}
	d, claim, view := b.Claim(issued.Code, who)
	if !d.Allowed || claim == "" || view.Server != "lab" || view.Operator != "saint" || view.From != "192.168.100.5" || view.Issued.IsZero() {
		t.Fatalf("claim = %+v %q %+v", d, claim, view)
	}
	if d, _, _ := b.Claim(issued.Code, Browser{Label: "Firefox on Linux", Address: "198.51.100.9"}); d.Allowed || d.Code != CodeTaken {
		t.Fatalf("second browser = %+v", d)
	}
	if watched, _ := b.Watch(issued.Poll); watched.State != Claimed || watched.Browser != who {
		t.Fatalf("terminal sees %+v", watched)
	}
	d, req, poll := b.Ceremony(claim)
	if !d.Allowed || req.Token != "enrol-token" || poll != issued.Poll {
		t.Fatalf("ceremony = %+v %+v %q", d, req, poll)
	}
	if d, _, _ := b.Ceremony("forged"); d.Allowed {
		t.Fatal("a forged claim token was accepted")
	}
	if d, reserved := b.Reserve(claim); !d.Allowed || reserved != poll {
		t.Fatalf("reserve = %+v %q", d, reserved)
	}
	b.Finish(poll, "Ana <ana@example.com>")
	if watched, _ := b.Watch(issued.Poll); watched.State != Done || watched.Account != "Ana <ana@example.com>" {
		t.Fatalf("after finish the terminal sees %+v", watched)
	}
	if d, _, _ := b.Ceremony(claim); d.Allowed {
		t.Fatal("a finished enrolment allowed another ceremony")
	}
	if b.Live() {
		t.Fatal("a finished enrolment is still live")
	}
}

// Once the passkey is being stored, neither the browser nor the host can
// cancel: a cancellation never reports success for a passkey that is stored
// anyway. A failed store releases the reservation and cancelling works again.
func TestStoringIsNotCancellable(t *testing.T) {
	b, _ := newBook()
	issued := issue(t, b)
	_, claim, _ := b.Claim(issued.Code, who)
	if d, _ := b.Reserve(claim); !d.Allowed {
		t.Fatalf("reserve = %+v", d)
	}
	if d := b.Withdraw(claim); d.Allowed || d.Code != CodeSaving {
		t.Fatalf("withdraw while saving = %+v", d)
	}
	if d := b.Cancel(issued.Poll); d.Allowed || d.Code != CodeSaving {
		t.Fatalf("host cancel while saving = %+v", d)
	}
	if d, _ := b.Reserve(claim); d.Allowed {
		t.Fatal("a second store was reserved")
	}
	b.Release(issued.Poll)
	if d := b.Withdraw(claim); !d.Allowed {
		t.Fatalf("withdraw after a failed store = %+v", d)
	}
	b.Finish(issued.Poll, "Eve <eve@example.com>")
	if watched, _ := b.Watch(issued.Poll); watched.State != Withdrawn {
		t.Fatalf("a withdrawn enrolment became %s", watched.State)
	}
}

// "This was not me" in the browser ends the code for everyone.
func TestWithdrawEndsTheEnrolment(t *testing.T) {
	b, _ := newBook()
	issued := issue(t, b)
	if d := b.Withdraw("forged"); d.Allowed {
		t.Fatal("a forged claim withdrew a code")
	}
	_, claim, _ := b.Claim(issued.Code, who)
	if d := b.Withdraw(claim); !d.Allowed {
		t.Fatalf("withdraw = %+v", d)
	}
	if d := b.Withdraw(claim); d.Allowed || d.Code != CodeEnded {
		t.Fatalf("second withdraw = %+v", d)
	}
	if d, _ := b.Reserve(claim); d.Allowed {
		t.Fatal("a withdrawn code was reserved")
	}
	if d, _, _ := b.Claim(issued.Code, who); d.Allowed {
		t.Fatal("a withdrawn code was claimed again")
	}
	if watched, _ := b.Watch(issued.Poll); watched.State != Withdrawn {
		t.Fatalf("terminal sees %s", watched.State)
	}
	if b.Live() {
		t.Fatal("a withdrawn code is still live")
	}
}

func TestDeadlines(t *testing.T) {
	b, c := newBook()
	issued := issue(t, b)
	c.advance(ClaimWindow)
	if d, _, _ := b.Claim(issued.Code, who); d.Allowed || d.Code != CodeExpired {
		t.Fatalf("claimed after the window: %+v", d)
	}
	if view, _ := b.Watch(issued.Poll); view.State != Expired {
		t.Fatalf("terminal sees %s after the window", view.State)
	}

	issued = issue(t, b)
	_, claim, _ := b.Claim(issued.Code, who)
	c.advance(Lifetime)
	if d, _, _ := b.Ceremony(claim); d.Allowed {
		t.Fatal("a claim outlived the enrolment")
	}
	c.advance(2 * time.Minute)
	if _, ok := b.Watch(issued.Poll); ok {
		t.Fatal("an old enrolment was never forgotten")
	}
}

// An address that guesses too often is refused, but the code survives: the
// right person on another network still uses it, and the terminal is told how
// many wrong codes were tried.
func TestGuessingFromOneAddressDoesNotEndTheCode(t *testing.T) {
	b, c := newBook()
	issued := issue(t, b)
	guesser := Browser{Label: "curl", Address: "198.51.100.66"}
	for range MaxAddressFailures {
		b.Claim("ZZZZ-ZZZZ", guesser)
	}
	if d, _, _ := b.Claim(issued.Code, guesser); d.Allowed || d.Code != CodeAddressLimit {
		t.Fatalf("the guessing address with the right code = %+v", d)
	}
	if view, _ := b.Watch(issued.Poll); view.State != Pending || view.Failures != MaxAddressFailures {
		t.Fatalf("terminal sees %+v", view)
	}
	if d, _, _ := b.Claim(issued.Code, who); !d.Allowed {
		t.Fatalf("the owner on another network = %+v", d)
	}
	c.advance(FailureWindow)
	fresh := issue(t, b)
	if d, _, _ := b.Claim(fresh.Code, guesser); !d.Allowed {
		t.Fatalf("the address after the window = %+v", d)
	}
}

// Too many wrong codes overall pause new claims for a while without touching
// any enrolment: a browser already creating its passkey continues, and the
// waiting code works once the pause ends.
func TestTooManyWrongCodesPauseClaimsWithoutEndingEnrolments(t *testing.T) {
	b, c := newBook()
	creating := issue(t, b)
	_, claim, _ := b.Claim(creating.Code, who)
	waiting := issue(t, b)
	for i := range MaxFailures {
		b.Claim("ZZZZ-ZZZZ", Browser{Address: fmt.Sprintf("198.51.100.%d", i)})
	}
	if d, _, _ := b.Claim(waiting.Code, who); d.Allowed || d.Code != CodePaused {
		t.Fatalf("claim during the pause = %+v", d)
	}
	if view, _ := b.Watch(waiting.Poll); !view.Paused {
		t.Fatal("the terminal is not told claims are paused")
	}
	if d, _, _ := b.Ceremony(claim); !d.Allowed {
		t.Fatalf("an enrolment in progress was ended by guessing: %+v", d)
	}
	c.advance(FailureWindow - time.Second)
	if d, _, _ := b.Claim(waiting.Code, who); d.Code != CodePaused {
		t.Fatalf("claim before the pause ends = %+v", d)
	}
	c.advance(time.Second)
	if d, _, _ := b.Claim(waiting.Code, who); d.Allowed {
		t.Fatal("the waiting code outlived its window")
	}
	again := issue(t, b)
	if d, _, _ := b.Claim(again.Code, who); !d.Allowed {
		t.Fatalf("after the pause = %+v", d)
	}
}

func TestMalformedInputCountsAsAFailure(t *testing.T) {
	b, _ := newBook()
	issue(t, b)
	for range MaxAddressFailures {
		b.Claim("not a code", who)
	}
	if d, _, _ := b.Claim("ABCD-EFGH", who); d.Code != CodeAddressLimit {
		t.Fatalf("after malformed attempts = %+v", d)
	}
}

// When the book is full Issue fails before anything else is touched, so the
// caller can issue first and store second.
func TestPendingIsBoundedAndCancelForgets(t *testing.T) {
	b, _ := newBook()
	var last Issued
	for range MaxPending {
		last = issue(t, b)
	}
	if _, err := b.Issue(Request{Kind: "bootstrap"}); err != ErrFull {
		t.Fatalf("issue beyond the bound = %v", err)
	}
	if d := b.Cancel(last.Poll); !d.Allowed {
		t.Fatalf("cancel = %+v", d)
	}
	if _, ok := b.Watch(last.Poll); ok {
		t.Fatal("a cancelled enrolment is still visible")
	}
	if d, _, _ := b.Claim(last.Code, who); d.Allowed {
		t.Fatal("a cancelled code was accepted")
	}
	if _, err := b.Issue(Request{Kind: "bootstrap"}); err != nil {
		t.Fatalf("issue after a cancel = %v", err)
	}
}

// Every symbol is used and none of the confusable ones appear.
func TestCodesUseTheWholeAlphabet(t *testing.T) {
	seen := map[byte]bool{}
	for range 200 {
		code, err := newCode(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < len(code); j++ {
			seen[code[j]] = true
		}
		if strings.ContainsAny(code, "IO01") {
			t.Fatalf("confusable symbol in %q", code)
		}
	}
	if len(seen) != len(Alphabet) {
		t.Fatalf("saw %d of %d symbols", len(seen), len(Alphabet))
	}
}

func FuzzNormalize(f *testing.F) {
	for _, seed := range []string{"ABCD-EFGH", "abcd efgh", "", "ABCD-EFG0", "ÄBCD-EFGH"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, typed string) {
		code, ok := Normalize(typed)
		if !ok {
			return
		}
		if len(code) != Length {
			t.Fatalf("Normalize(%q) = %q", typed, code)
		}
		for _, r := range code {
			if !strings.ContainsRune(Alphabet, r) {
				t.Fatalf("Normalize(%q) = %q has %q", typed, code, r)
			}
		}
		again, ok := Normalize(Display(code))
		if !ok || again != code {
			t.Fatalf("display round trip of %q = %q", code, again)
		}
	})
}

// A newer recovery for the same account, or the first administrator being
// created, ends the codes that can no longer succeed; their terminals see it.
func TestSupersedeEndsCodesThatCanNoLongerSucceed(t *testing.T) {
	b, _ := newBook()
	older, _ := b.Issue(Request{Kind: "recover", Email: "ana@example.com"})
	other, _ := b.Issue(Request{Kind: "recover", Email: "eve@example.com"})
	newer, _ := b.Issue(Request{Kind: "recover", Email: "ana@example.com"})
	b.Supersede(newer.Poll, "recover", "ana@example.com")
	if view, _ := b.Watch(older.Poll); view.State != Replaced {
		t.Fatalf("older recovery is %s", view.State)
	}
	if d, _, _ := b.Claim(older.Code, who); d.Allowed || d.Code != CodeEnded {
		t.Fatalf("claim of a replaced code = %+v", d)
	}
	for _, poll := range []string{other.Poll, newer.Poll} {
		if view, _ := b.Watch(poll); view.State != Pending {
			t.Fatalf("an unrelated or the newest code became %s", view.State)
		}
	}
}
