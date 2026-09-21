package auth

import "testing"

func TestPendingPutAndTake(t *testing.T) {
	p := newPendingStore()
	p.put("state1", "verifier1", "/dashboard")

	pl, ok := p.take("state1")
	if !ok {
		t.Fatal("expected pending login to be found")
	}
	if pl.codeVerifier != "verifier1" || pl.returnTo != "/dashboard" {
		t.Errorf("unexpected pending login: %+v", pl)
	}
}

func TestPendingTakeIsOneTimeUse(t *testing.T) {
	p := newPendingStore()
	p.put("state1", "verifier1", "/dashboard")

	p.take("state1")
	if _, ok := p.take("state1"); ok {
		t.Fatal("expected second take of the same state to fail")
	}
}

func TestPendingTakeUnknownState(t *testing.T) {
	p := newPendingStore()
	if _, ok := p.take("never-put"); ok {
		t.Fatal("expected unknown state to not be found")
	}
}
