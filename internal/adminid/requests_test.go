package adminid

import (
	"errors"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// counter is a deterministic byte source: every byte differs from the last, so
// IDs and secrets never repeat within a test.
type counter struct{ n byte }

func (c *counter) Read(p []byte) (int, error) {
	for i := range p {
		c.n++
		p[i] = c.n
	}
	return len(p), nil
}

func newBook() (*Requests, *clock) {
	c := &clock{t: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	return NewRequestsAt(c.now, &counter{}), c
}

func TestRequestApprovalIsDeliveredOnlyToTheHolderOfTheSecret(t *testing.T) {
	book, _ := newBook()
	id, secret, _, err := book.Submit("Ana", "ana@example.com", "Firefox", false)
	if err != nil {
		t.Fatal(err)
	}
	if state, token, err := book.Poll(id, secret); err != nil || state != RequestPending || token != "" {
		t.Fatalf("poll before a decision = %v %q %v", state, token, err)
	}
	if err := book.Approve(id, "link-token"); err != nil {
		t.Fatal(err)
	}
	// Someone who sees the ID but lacks the secret learns nothing.
	if _, token, err := book.Poll(id, "guess"); !errors.Is(err, ErrRequestGone) || token != "" {
		t.Fatalf("poll with the wrong secret = %q %v, want gone", token, err)
	}
	state, token, err := book.Poll(id, secret)
	if err != nil || state != RequestApproved || token != "link-token" {
		t.Fatalf("poll after approval = %v %q %v", state, token, err)
	}
	if _, _, err := book.Poll(id, secret); !errors.Is(err, ErrRequestGone) {
		t.Fatalf("the link was delivered twice: %v", err)
	}
}

func TestDeniedRequestIsReportedOnceAndNeverApprovable(t *testing.T) {
	book, _ := newBook()
	id, secret, _, _ := book.Submit("Eve", "eve@example.com", "", false)
	if err := book.Deny(id); err != nil {
		t.Fatal(err)
	}
	if err := book.Approve(id, "x"); !errors.Is(err, ErrRequestGone) {
		t.Fatalf("approving a denied request = %v", err)
	}
	if state, _, err := book.Poll(id, secret); err != nil || state != RequestDenied {
		t.Fatalf("poll after denial = %v %v", state, err)
	}
	if _, _, err := book.Poll(id, secret); !errors.Is(err, ErrRequestGone) {
		t.Fatal("a denial was reported twice")
	}
}

func TestHiddenRequestLooksPendingButNeverReachesAnAdministrator(t *testing.T) {
	book, _ := newBook()
	id, secret, _, err := book.Submit("Admin", "owner@example.com", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := book.Pending(); len(got) != 0 {
		t.Fatalf("a hidden request was listed: %v", got)
	}
	if _, err := book.Get(id); !errors.Is(err, ErrRequestGone) {
		t.Fatal("a hidden request could be reviewed")
	}
	if err := book.Approve(id, "x"); !errors.Is(err, ErrRequestGone) {
		t.Fatal("a hidden request could be approved")
	}
	if state, _, err := book.Poll(id, secret); err != nil || state != RequestPending {
		t.Fatalf("a hidden request answers %v %v, want pending like any other", state, err)
	}
}

func TestRequestsExpireAndAnApprovalLinkLivesOnlyBriefly(t *testing.T) {
	book, clk := newBook()
	id, secret, _, _ := book.Submit("Ana", "ana@example.com", "", false)
	clk.t = clk.t.Add(RequestTTL)
	if _, _, err := book.Poll(id, secret); !errors.Is(err, ErrRequestGone) {
		t.Fatalf("an expired request still answers: %v", err)
	}
	id, secret, _, _ = book.Submit("Ana", "ana@example.com", "", false)
	if err := book.Approve(id, "link"); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(ApprovedTTL)
	if _, token, err := book.Poll(id, secret); !errors.Is(err, ErrRequestGone) || token != "" {
		t.Fatalf("an uncollected link outlived its token: %q %v", token, err)
	}
}

func TestSameEmailReplacesItsEarlierRequest(t *testing.T) {
	book, _ := newBook()
	first, firstSecret, _, _ := book.Submit("Ana", "ana@example.com", "", false)
	second, _, _, _ := book.Submit("Ana P", "ana@example.com", "", false)
	if got := book.Pending(); len(got) != 1 || got[0].ID != second {
		t.Fatalf("pending = %v, want only the newest", got)
	}
	if _, _, err := book.Poll(first, firstSecret); !errors.Is(err, ErrRequestGone) {
		t.Fatal("the replaced request still answers")
	}
}

func TestRequestsAreRateLimitedAndCapped(t *testing.T) {
	book, clk := newBook()
	for i := 0; i < maxSubmitsInWindow; i++ {
		if _, _, _, err := book.Submit("N", string(rune('a'+i))+"@example.com", "", false); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	if _, _, _, err := book.Submit("N", "z@example.com", "", false); !errors.Is(err, ErrRequestBusy) {
		t.Fatalf("an eleventh request in the window = %v, want busy", err)
	}
	clk.t = clk.t.Add(submitWindow)
	if _, _, _, err := book.Submit("N", "z@example.com", "", false); err != nil {
		t.Fatalf("after the window: %v", err)
	}
	// Spread over time, the waiting list is still bounded.
	book, clk = newBook()
	for i := 0; i < maxPendingRequests; i++ {
		clk.t = clk.t.Add(submitWindow)
		if _, _, _, err := book.Submit("N", "p"+string(rune('a'+i%26))+string(rune('a'+i/26))+"@example.com", "", false); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	clk.t = clk.t.Add(submitWindow)
	if _, _, _, err := book.Submit("N", "late@example.com", "", false); !errors.Is(err, ErrRequestBusy) {
		t.Fatalf("a request past the cap = %v, want busy", err)
	}
}
