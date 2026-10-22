package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/notify"
)

// pairPoll is how often `notify pair` asks whether the chat answered.
var pairPoll = time.Second

// runNotify implements `switchboard notify pair`: link one Telegram chat to
// this deployment with a one-time code.
//
// It is a client of the admin API on the unix socket (CLI-001): the running
// process makes the code, reads the bot's updates (Telegram allows one
// reader) and records the chat, so the pairing shows in Activity and
// alerts every chat already paired. The code lives only in the process and
// stops working at -timeout or when this command is interrupted; nothing is
// sent to the chat.
func runNotify(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard notify pair [-config config.json] [-admin unix:/path] [-timeout 10m]")
	}
	switch args[0] {
	case "pair":
		return runNotifyPair(ctx, args[1:], out)
	default:
		return fmt.Errorf("unknown notify subcommand %q", args[0])
	}
}

type pairingAnswer struct {
	State   notify.PairingState `json:"state"`
	ChatID  int64               `json:"chat_id"`
	Expires time.Time           `json:"expires"`
	Code    string              `json:"code"`
}

func runNotifyPair(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("notify pair", flag.ContinueOnError)
	fs.SetOutput(out)
	af := addAdminFlags(fs)
	wait := fs.Duration("timeout", 10*time.Minute, "how long the code waits for the /pair message (at most 1h)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *wait < time.Second || *wait > time.Hour {
		return errors.New("-timeout must be between 1s and 1h")
	}
	c, err := af.client()
	if err != nil {
		return err
	}

	var started pairingAnswer
	if err := c.do(ctx, http.MethodPost, "/_/api/notify/pair", map[string]int{"timeout_seconds": int(wait.Seconds())}, &started); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return errors.New("the running edge has no notify.telegram configured")
		}
		return err
	}
	fmt.Fprintln(out, notify.PairingText(started.Code))
	fmt.Fprintf(out, "Waiting until %s; the code stops working then, or when this command is interrupted.\n", started.Expires.Format(time.RFC3339))

	ticker := time.NewTicker(pairPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// The interrupted context cannot carry the cancellation itself.
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := c.do(cancelCtx, http.MethodDelete, "/_/api/notify/pair", nil, nil); err != nil && !isStatus(err, http.StatusNotFound) {
				return fmt.Errorf("interrupted, and the pairing could not be cancelled (it expires at %s): %w", started.Expires.Format(time.RFC3339), err)
			}
			return errors.New("interrupted: the pairing was cancelled; nothing was paired")
		case <-ticker.C:
		}
		var status pairingAnswer
		if err := c.do(ctx, http.MethodGet, "/_/api/notify/pair", nil, &status); err != nil {
			if ctx.Err() != nil {
				continue // the select above cancels the pairing
			}
			return err
		}
		switch status.State {
		case notify.PairingWaiting:
		case notify.PairingPaired:
			fmt.Fprintf(out, "Paired chat %d; Switchboard alerts will go there.\n", status.ChatID)
			return nil
		case notify.PairingExpired:
			return errors.New("the pairing code expired unused; nothing was paired")
		case notify.PairingCancelled:
			return errors.New("the pairing was cancelled; nothing was paired")
		default:
			return fmt.Errorf("the pairing ended as %q; see the edge's log", status.State)
		}
	}
}
