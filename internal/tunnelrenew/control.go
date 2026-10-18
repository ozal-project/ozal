package tunnelrenew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func save(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".renew-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

type pending struct {
	Rotations []Transaction `json:"rotations"`
}

func renew(ctx context.Context, hosts Hosts, home, directory string, force bool, out io.Writer) error {
	journal := filepath.Join(directory, "pending.json")
	engine := Rotation{Hosts: hosts}
	complete := func(transactions []Transaction) error {
		if err := engine.Apply(ctx, transactions); err != nil {
			if rollbackErr := engine.Rollback(ctx, transactions); rollbackErr != nil {
				return fmt.Errorf("renewal failed and rollback incomplete; pending journal retained: %w", errors.Join(err, rollbackErr))
			}
			if removeErr := os.Remove(journal); removeErr != nil {
				return removeErr
			}
			if syncErr := syncDirectory(directory); syncErr != nil {
				return syncErr
			}
			return fmt.Errorf("renewal failed; previous identities restored: %w", err)
		}
		if err := os.Remove(journal); err != nil {
			return err
		}
		if err := syncDirectory(directory); err != nil {
			return err
		}
		fmt.Fprintln(out, "Renewal verified; active identities replaced")
		return nil
	}
	if data, err := os.ReadFile(journal); err == nil {
		var batch pending
		if err := json.Unmarshal(data, &batch); err != nil {
			return errors.New("invalid pending renewal journal")
		}
		if len(batch.Rotations) == 0 {
			return errors.New("empty pending renewal journal")
		}
		fmt.Fprintln(out, "Resuming interrupted renewal")
		if err := complete(batch.Rotations); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	ca := filepath.Join(home, ".local/share/switchboard-tunnel-ca")
	caInfo, err := certificate(filepath.Join(ca, "ca.crt"))
	if err != nil {
		return err
	}
	if remaining := time.Until(caInfo.Expires); remaining < 60*day {
		fmt.Fprintf(out, "CA rollover required soon (%d days); trust anchors are not replaced automatically\n", int(remaining/day))
	}
	var old []Identity
	due := force
	for _, role := range []string{"edge", "connector"} {
		reply, err := hosts.Call(ctx, role, Request{Action: "status"})
		if err != nil {
			return err
		}
		old = append(old, reply.Identity)
		remaining := time.Until(reply.Identity.Expires)
		if remaining <= 7*day {
			due = true
		}
		fmt.Fprintf(out, "Certificate %s: %d days remaining\n", role, int(remaining/day))
	}
	if !due {
		return nil
	}
	var transactions []Transaction
	for i, role := range []string{"edge", "connector"} {
		fmt.Fprintf(out, "Preparing renewal: %s\n", role)
		id, err := nonce()
		if err != nil {
			return err
		}
		prepared, err := hosts.Call(ctx, role, Request{Action: "prepare", ID: id})
		if err != nil {
			return err
		}
		signed, err := issue(ca, role, prepared.CSR, time.Now())
		if err != nil {
			return err
		}
		certified, err := hosts.Call(ctx, role, Request{Action: "certify", Link: prepared.Identity.Link, Certificate: signed})
		if err != nil {
			return err
		}
		transactions = append(transactions, Transaction{Role: role, Old: old[i], New: certified.Identity})
	}
	if err := save(journal, pending{Rotations: transactions}); err != nil {
		return err
	}
	return complete(transactions)
}

func Run(ctx context.Context, hosts Hosts, home string, force, initialize bool, out io.Writer) error {
	directory := filepath.Join(home, ".local/share/switchboard-tunnel-renew")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	release, err := acquireLock(filepath.Join(directory, "lock"))
	if err != nil {
		return err
	}
	defer release()
	if initialize {
		for _, role := range []string{"edge", "connector"} {
			if _, err := hosts.Call(ctx, role, Request{Action: "bootstrap"}); err != nil {
				return err
			}
		}
		fmt.Fprintln(out, "Identity pointers initialized; services unchanged")
		return nil
	}
	err = renew(ctx, hosts, home, directory, force, out)
	status := struct {
		OK      bool      `json:"ok"`
		Time    time.Time `json:"time"`
		Failure bool      `json:"failure"`
	}{OK: err == nil, Time: time.Now(), Failure: err != nil}
	if statusErr := save(filepath.Join(directory, "status.json"), status); statusErr != nil {
		return errors.Join(err, statusErr)
	}
	return err
}

func ServeAgent(ctx context.Context, agent Agent, in io.Reader, out io.Writer) error {
	var request Request
	decoder := json.NewDecoder(io.LimitReader(in, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return errors.New("invalid host request")
	}
	reply, err := agent.Handle(ctx, request)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(reply)
}
