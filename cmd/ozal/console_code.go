package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// codePollInterval is how often the terminal checks on its enrolment.
const codePollInterval = time.Second

type codeView struct {
	State    string    `json:"state"`
	Failures int       `json:"failures"`
	Paused   bool      `json:"paused"`
	Account  string    `json:"account"`
	Browser  string    `json:"browser"`
	Address  string    `json:"address"`
	Country  string    `json:"country"`
	Claimed  time.Time `json:"claimed"`
}

// enrolByCode shows a code to type on the login host, where the rest of the
// enrolment happens, and reports how it ends. The terminal asks nothing: it
// says which browser used the code and who was enrolled, and Ctrl-C withdraws
// the code. It reports false, with no error, when the edge has no login host
// for codes, so the caller falls back to a link.
func enrolByCode(ctx context.Context, c *adminClient, kind, email string, out io.Writer) (bool, error) {
	var issued struct {
		Poll    string    `json:"poll"`
		Code    string    `json:"code"`
		URL     string    `json:"url"`
		ClaimBy time.Time `json:"claim_by"`
	}
	request := map[string]string{"kind": kind, "email": email, "mode": "code", "operator": invokingUser(), "from": sshClient(os.Getenv("SSH_CONNECTION"))}
	if err := c.do(ctx, http.MethodPost, "/_/api/admin/enrolments", request, &issued); err != nil {
		// 412: codes need admin.login_host. 400: an edge from before codes
		// refuses the new fields. Either way the link still works.
		if isStatus(err, http.StatusPreconditionFailed) || isStatus(err, http.StatusBadRequest) {
			return false, nil
		}
		return false, err
	}
	watchPath := "/_/api/admin/enrolments/code/" + url.PathEscape(issued.Poll)
	finished := false
	defer func() {
		if !finished {
			// Interrupted or failed: the code must not stay usable.
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = c.do(cancelCtx, http.MethodDelete, watchPath, nil, nil)
		}
	}()

	what := "Create the first administrator"
	if kind == "recover" {
		what = "Replace the passkeys of " + email
	}
	fmt.Fprintf(out, "%s:\n\n  Open   %s\n  Code   %s\n\nThe code works once, until %s. Everything else happens on that page.\nWaiting… (Ctrl-C cancels the code)\n",
		what, issued.URL, issued.Code, issued.ClaimBy.Local().Format("15:04"))

	reported, warned, paused := false, 0, false
	ticker := time.NewTicker(codePollInterval)
	defer ticker.Stop()
	for {
		var view codeView
		if err := c.do(ctx, http.MethodGet, watchPath, nil, &view); err != nil {
			if isStatus(err, http.StatusNotFound) {
				return true, errors.New("the enrolment expired; run the command again")
			}
			return true, err
		}
		if view.Failures > warned {
			warned = view.Failures
			fmt.Fprintf(out, "Warning: %d wrong code(s) typed on the login host so far. If that was not you, someone is guessing; the code still works for you.\n", view.Failures)
		}
		if view.Paused && !paused {
			fmt.Fprintln(out, "Many wrong codes were typed on the login host, so new codes are paused for a few minutes; yours included. If it goes on, press Ctrl-C and use the link instead: add -link.")
		}
		paused = view.Paused
		switch view.State {
		case "claimed":
			if !reported {
				reported = true
				fmt.Fprintf(out, "Code entered in %s from %s at %s; finishing on the page…\n",
					orUnknown(view.Browser, "an unknown browser"), withCountry(orUnknown(view.Address, "an unknown address"), view.Country), view.Claimed.Local().Format("15:04:05"))
			}
		case "done":
			finished = true
			fmt.Fprintf(out, "Passkey created for %s.\n", orUnknown(view.Account, "the administrator"))
			return true, nil
		case "withdrawn":
			return true, errors.New("the code was cancelled on the page (\"this wasn't me\"); nothing was created")
		case "expired":
			return true, errors.New("the code expired; run the command again")
		case "replaced":
			return true, errors.New("a newer code or link for the same account replaced this one; use the latest")
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-ticker.C:
		}
	}
}

// sshClient is the address an SSH session came from, as SSH_CONNECTION
// reports it ("client port server port"); empty on a local terminal.
func sshClient(connection string) string {
	if fields := strings.Fields(connection); len(fields) == 4 {
		return fields[0]
	}
	return ""
}

// withCountry appends the approximate country the edge's local table gave.
func withCountry(address, country string) string {
	if country == "" {
		return address
	}
	return address + " (" + country + ")"
}

func orUnknown(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
