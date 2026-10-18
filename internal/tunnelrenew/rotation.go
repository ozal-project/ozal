package tunnelrenew

import (
	"context"
	"errors"
)

type Request struct {
	Action      string `json:"action"`
	ID          string `json:"id,omitempty"`
	Link        string `json:"link,omitempty"`
	Expected    string `json:"expected,omitempty"`
	Certificate string `json:"certificate,omitempty"`
}

type Reply struct {
	Identity Identity `json:"identity"`
	CSR      string   `json:"csr,omitempty"`
	Active   string   `json:"active,omitempty"`
	Digest   string   `json:"digest,omitempty"`
}

type Hosts interface {
	Call(context.Context, string, Request) (Reply, error)
	Healthy(context.Context) error
}

type Transaction struct {
	Role string   `json:"role"`
	Old  Identity `json:"old"`
	New  Identity `json:"new"`
}

type Rotation struct{ Hosts Hosts }

func (r Rotation) identity(ctx context.Context, tx Transaction) (Identity, error) {
	current, err := r.Hosts.Call(ctx, tx.Role, Request{Action: "status"})
	if err != nil {
		return Identity{}, err
	}
	for _, known := range []Identity{tx.Old, tx.New} {
		if current.Identity.Link == known.Link && current.Identity.Fingerprint == known.Fingerprint {
			return current.Identity, nil
		}
	}
	return Identity{}, errors.New("operator changed identity; refusing to overwrite it")
}

func (r Rotation) apply(ctx context.Context, tx Transaction, rollback bool) error {
	current, err := r.identity(ctx, tx)
	if err != nil {
		return err
	}
	from, to := tx.Old, tx.New
	if rollback {
		from, to = tx.New, tx.Old
	}
	if tx.Role == "connector" {
		active, err := r.Hosts.Call(ctx, "edge", Request{Action: "active"})
		if err != nil {
			return err
		}
		if active.Active == from.Fingerprint {
			if _, err := r.Hosts.Call(ctx, "edge", Request{Action: "rotate", Expected: active.Active, Certificate: to.Certificate}); err != nil {
				return err
			}
		} else if active.Active != to.Fingerprint {
			return errors.New("operator changed or revoked enrolment; refusing rotation/rollback")
		}
	}
	if current.Link == from.Link {
		if _, err := r.Hosts.Call(ctx, tx.Role, Request{Action: "switch", Expected: current.Link, Link: to.Link}); err != nil {
			return err
		}
	}
	_, err = r.Hosts.Call(ctx, tx.Role, Request{Action: "restart"})
	return err
}

// Apply restores both roles before probing: both leaves may have expired while
// offline. Each step is idempotent after a lost SSH reply or controller crash.
func (r Rotation) Apply(ctx context.Context, transactions []Transaction) error {
	for _, tx := range transactions {
		if err := r.apply(ctx, tx, false); err != nil {
			return err
		}
	}
	return r.Hosts.Healthy(ctx)
}

func (r Rotation) Rollback(ctx context.Context, transactions []Transaction) error {
	var failures []error
	for i := len(transactions) - 1; i >= 0; i-- {
		if err := r.apply(ctx, transactions[i], true); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	return r.Hosts.Healthy(ctx)
}
