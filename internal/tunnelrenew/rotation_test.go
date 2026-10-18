package tunnelrenew

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeHosts struct {
	roles                                          map[string]Identity
	active                                         string
	rotations                                      int
	lostReply, offline, failHealth, revokeOnHealth bool
}

func fixture() (*fakeHosts, []Transaction) {
	h := &fakeHosts{roles: map[string]Identity{}, active: "old"}
	var txs []Transaction
	for _, role := range []string{"edge", "connector"} {
		old := Identity{Link: "identity", Fingerprint: "old", Certificate: "old", Expires: time.Now().Add(60 * day)}
		next := Identity{Link: "identities/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Fingerprint: "new", Certificate: "new", Expires: old.Expires}
		h.roles[role] = old
		txs = append(txs, Transaction{Role: role, Old: old, New: next})
	}
	return h, txs
}

func (h *fakeHosts) Call(_ context.Context, role string, req Request) (Reply, error) {
	if h.offline {
		return Reply{}, errors.New("SSH unavailable")
	}
	switch req.Action {
	case "status":
		return Reply{Identity: h.roles[role]}, nil
	case "active":
		return Reply{Active: h.active}, nil
	case "rotate":
		if req.Expected != h.active {
			return Reply{}, errors.New("registry CAS conflict")
		}
		h.active = req.Certificate
		h.rotations++
		if h.lostReply {
			h.lostReply = false
			return Reply{}, errors.New("committed but lost reply")
		}
	case "switch":
		old := h.roles[role]
		if old.Link != req.Expected {
			return Reply{}, errors.New("pointer conflict")
		}
		fp := "new"
		if req.Link == "identity" {
			fp = "old"
		}
		h.roles[role] = Identity{Link: req.Link, Fingerprint: fp, Certificate: fp, Expires: old.Expires}
	case "restart":
	default:
		return Reply{}, errors.New("unexpected test action")
	}
	return Reply{}, nil
}

func (h *fakeHosts) Healthy(context.Context) error {
	if h.revokeOnHealth {
		h.active = "none"
		return errors.New("operator revoked during verification")
	}
	if h.failHealth {
		h.failHealth = false
		return errors.New("new service failed")
	}
	if h.roles["edge"].Fingerprint != h.roles["connector"].Fingerprint {
		return errors.New("both roles must recover before health can pass")
	}
	return nil
}

func TestResumeAfterRegistryCommitLostReply(t *testing.T) {
	h, txs := fixture()
	h.lostReply = true
	r := Rotation{Hosts: h}
	if err := r.Apply(context.Background(), txs); err == nil {
		t.Fatal("lost reply not surfaced")
	}
	if err := r.Apply(context.Background(), txs); err != nil {
		t.Fatal(err)
	}
	if h.rotations != 1 || h.roles["connector"].Fingerprint != "new" {
		t.Fatal("resume duplicated rotation or lost identity")
	}
}

func TestFailedBatchRestoresOldPair(t *testing.T) {
	h, txs := fixture()
	h.failHealth = true
	r := Rotation{Hosts: h}
	if err := r.Apply(context.Background(), txs); err == nil {
		t.Fatal("health failure missing")
	}
	if err := r.Rollback(context.Background(), txs); err != nil {
		t.Fatal(err)
	}
	if h.active != "old" || h.roles["edge"].Fingerprint != "old" || h.roles["connector"].Fingerprint != "old" {
		t.Fatal("rollback did not restore both roles")
	}
}

func TestRollbackNeverOverridesOperatorRevocation(t *testing.T) {
	h, txs := fixture()
	h.revokeOnHealth = true
	r := Rotation{Hosts: h}
	if err := r.Apply(context.Background(), txs); err == nil {
		t.Fatal("expected revoke")
	}
	if err := r.Rollback(context.Background(), txs); err == nil {
		t.Fatal("rollback undid external revocation")
	}
	if h.active != "none" || h.rotations != 1 {
		t.Fatal("revoked access was restored")
	}
}

func TestRotationRejectsExternallyReplacedFile(t *testing.T) {
	h, txs := fixture()
	changed := h.roles["edge"]
	changed.Fingerprint = "operator"
	h.roles["edge"] = changed
	if err := (Rotation{Hosts: h}).Apply(context.Background(), txs); err == nil {
		t.Fatal("operator replacement overwritten")
	}
	if h.rotations != 0 {
		t.Fatal("registry mutated before validation")
	}
}

func TestBothExpiredLeavesRecoverAsOneBatch(t *testing.T) {
	h, txs := fixture()
	for role, identity := range h.roles {
		identity.Expires = time.Now().Add(-day)
		h.roles[role] = identity
	}
	if err := (Rotation{Hosts: h}).Apply(context.Background(), txs); err != nil {
		t.Fatal(err)
	}
	if h.active != "new" {
		t.Fatal("expired connector not replaced")
	}
}
