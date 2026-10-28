package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/passkey"
	"github.com/switchboard-project/switchboard/internal/store/sqlite"
)

const (
	defaultInviteTTL = 15 * time.Minute
	maxInviteTTL     = 24 * time.Hour
)

// userArgs are the parsed flags of `switchboard user`.
type userArgs struct {
	sub, email, name, prefix, returnTo string
	groups                             []string
	recoverAccess                      bool
	ttl                                time.Duration
}

// runUser implements `switchboard user invite|list|passkeys|revoke|disable|enable`:
// the operator's side of passkey login (ID-003). It is a client of the
// admin API (CLI-001), on the admin unix socket by default; recovery
// (-recover) is accepted only there, the shell being the root of trust.
// -db is the offline break-glass for a process that cannot start.
func runUser(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard user invite|list|passkeys|revoke|disable|enable [flags]")
	}
	a := userArgs{sub: args[0]}
	fs := flag.NewFlagSet("user "+a.sub, flag.ContinueOnError)
	fs.SetOutput(out)
	af := addAdminFlags(fs)
	dbPath := fs.String("db", "", "offline break-glass: change this SQLite state database directly, bypassing the running process")
	fs.StringVar(&a.email, "email", "", "the user's email address")
	fs.StringVar(&a.name, "name", "", "display name shown by the authenticator (invite)")
	fs.BoolVar(&a.recoverAccess, "recover", false, "invite a user who already has a passkey to add another (lost device; host only)")
	fs.DurationVar(&a.ttl, "ttl", defaultInviteTTL, "how long the enrolment link stays valid (at most 24h)")
	fs.StringVar(&a.prefix, "passkey", "", "leading hex characters of the passkey ID (revoke)")
	fs.StringVar(&a.returnTo, "return-to", "", "invite: protected URL to open after the passkey is created (must be a configured host)")
	fs.Func("group", "group the user belongs to, for access policies (repeatable)", func(v string) error {
		v = strings.TrimSpace(v)
		if v == "" {
			return errors.New("empty group")
		}
		a.groups = append(a.groups, v)
		return nil
	})
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	switch a.sub {
	case "invite", "list", "passkeys", "revoke", "disable", "enable":
	default:
		return fmt.Errorf("unknown user command %q", a.sub)
	}
	if a.sub != "list" && a.email == "" {
		return fmt.Errorf("%s needs -email", a.sub)
	}
	if a.sub == "revoke" && a.prefix == "" {
		return errors.New("revoke needs -passkey")
	}
	if a.sub == "invite" && (a.ttl <= 0 || a.ttl > maxInviteTTL) {
		return fmt.Errorf("-ttl must be between 1s and %s", maxInviteTTL)
	}
	if *dbPath != "" {
		return runUserOffline(ctx, a, *dbPath, *af.config, out)
	}
	client, err := af.client()
	if err != nil {
		return err
	}
	return runUserAPI(ctx, client, a, out)
}

// person is the admin API's view of a user (internal/admin/people.go).
type person struct {
	Email    string    `json:"email"`
	Subject  string    `json:"subject"`
	Groups   []string  `json:"groups"`
	Disabled time.Time `json:"disabled"`
	Passkeys []struct {
		ID       string    `json:"id"`
		Synced   bool      `json:"synced"`
		Created  time.Time `json:"created"`
		LastUsed time.Time `json:"last_used"`
		Revoked  time.Time `json:"revoked"`
	} `json:"passkeys"`
}

func runUserAPI(ctx context.Context, c *adminClient, a userArgs, out io.Writer) error {
	personPath := "/_/api/people/" + url.PathEscape(a.email)
	switch a.sub {
	case "invite":
		var invited struct {
			Email     string    `json:"email"`
			Link      string    `json:"link"`
			ExpiresAt time.Time `json:"expires_at"`
		}
		request := map[string]any{
			"email": a.email, "name": a.name, "groups": a.groups, "recover": a.recoverAccess,
			"return_to": a.returnTo, "ttl_minutes": int(math.Ceil(a.ttl.Minutes())),
		}
		if err := c.do(ctx, http.MethodPost, "/_/api/people/invite", request, &invited); err != nil {
			return err
		}
		printInvite(out, invited.Email, invited.Link, invited.ExpiresAt)
	case "list", "passkeys":
		var listed struct {
			Users []person `json:"users"`
		}
		if err := c.do(ctx, http.MethodGet, "/_/api/people", nil, &listed); err != nil {
			return err
		}
		if a.sub == "list" {
			for _, u := range listed.Users {
				fmt.Fprintf(out, "%s\t%s\t%s\tgroups=%s\n", u.Email, u.Subject, enabledState(u.Disabled), strings.Join(u.Groups, ","))
			}
			return nil
		}
		for _, u := range listed.Users {
			if !strings.EqualFold(u.Email, a.email) {
				continue
			}
			for _, k := range u.Passkeys {
				fmt.Fprintf(out, "%s\t%s\tcreated=%s\tlast_used=%s\t%s\n",
					shortID(k.ID), kindName(k.Synced), day(k.Created), day(k.LastUsed), revokedAt(k.Revoked))
			}
			return nil
		}
		return fmt.Errorf("no user %s", a.email)
	case "revoke":
		if err := c.do(ctx, http.MethodPost, personPath+"/passkeys/"+url.PathEscape(a.prefix)+"/revoke", nil, &struct{}{}); err != nil {
			return apiUserError(err, a.email)
		}
		fmt.Fprintf(out, "revoked passkey %s of %s; the user's sessions are closed\n", a.prefix, a.email)
	case "disable", "enable":
		if err := c.do(ctx, http.MethodPost, personPath+"/"+a.sub, nil, &struct{}{}); err != nil {
			return apiUserError(err, a.email)
		}
		printEnabled(out, a.sub, a.email)
	}
	return nil
}

// runUserOffline is the break-glass path: the state database, directly.
func runUserOffline(ctx context.Context, a userArgs, dbPath, configPath string, out io.Writer) error {
	db, err := openMigratedDB(ctx, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	identity := sqlite.NewIdentity(db)
	var sink events.Sink = events.Discard
	if a.sub != "list" && a.sub != "passkeys" {
		sink = offlineNotice()
	}

	switch a.sub {
	case "invite":
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}
		if cfg.Auth.Passkeys == nil {
			return fmt.Errorf("%s has no auth.passkeys section", configPath)
		}
		if _, err := passkey.InviteLink(*cfg, "", a.returnTo); err != nil {
			return fmt.Errorf("-%w", err)
		}
		invitation, err := identity.Invite(ctx, a.email, a.name, a.groups, a.recoverAccess, a.ttl)
		if err != nil {
			return err
		}
		link, err := passkey.InviteLink(*cfg, invitation.Token, a.returnTo)
		if err != nil {
			return err
		}
		code := "invite"
		if a.recoverAccess {
			code = "recover"
		}
		emitOffline(sink, events.Event{Type: events.UserInvited, Host: invitation.User.Email, Code: code})
		printInvite(out, invitation.User.Email, link, invitation.ExpiresAt)
	case "list":
		users, err := identity.Users(ctx)
		if err != nil {
			return err
		}
		for _, u := range users {
			fmt.Fprintf(out, "%s\t%s\t%s\tgroups=%s\n", u.Email, u.Subject(), enabledState(u.DisabledAt), strings.Join(u.Groups, ","))
		}
	case "passkeys":
		keys, err := identity.Passkeys(ctx, a.email)
		if err != nil {
			return userError(err, a.email)
		}
		for _, k := range keys {
			fmt.Fprintf(out, "%s\t%s\tcreated=%s\tlast_used=%s\t%s\n",
				shortID(hex.EncodeToString(k.Credential.ID)), kindName(k.Synced()), day(k.CreatedAt), day(k.LastUsedAt), revokedAt(k.RevokedAt))
		}
	case "revoke":
		if err := identity.RevokePasskey(ctx, a.email, a.prefix); err != nil {
			return userError(err, a.email)
		}
		emitOffline(sink, events.Event{Type: events.PasskeyRevoked, Host: a.email, Reason: "revoked by the operator"})
		fmt.Fprintf(out, "revoked passkey %s of %s; the user's sessions are closed\n", a.prefix, a.email)
	case "disable", "enable":
		if err := identity.SetDisabled(ctx, a.email, a.sub == "disable"); err != nil {
			return userError(err, a.email)
		}
		kind := events.UserEnabled
		if a.sub == "disable" {
			kind = events.UserDisabled
		}
		emitOffline(sink, events.Event{Type: kind, Host: a.email})
		printEnabled(out, a.sub, a.email)
	}
	return nil
}

func printInvite(out io.Writer, email, link string, expires time.Time) {
	fmt.Fprintf(out, "Enrolment link for %s (single use, expires %s):\n\n  %s\n\n",
		email, expires.UTC().Format(time.RFC3339), link)
	fmt.Fprintln(out, "Send it over a channel you trust: whoever opens it first gets the passkey.")
}

func printEnabled(out io.Writer, sub, email string) {
	if sub == "disable" {
		fmt.Fprintf(out, "disabled %s; the user's sessions are closed\n", email)
	} else {
		fmt.Fprintf(out, "enabled %s\n", email)
	}
}

func userError(err error, email string) error {
	if errors.Is(err, passkey.ErrNotFound) {
		return fmt.Errorf("no user %s", email)
	}
	return err
}

func apiUserError(err error, email string) error {
	if isStatus(err, http.StatusNotFound) {
		return fmt.Errorf("no user %s", email)
	}
	return err
}

func enabledState(disabled time.Time) string {
	if disabled.IsZero() {
		return "enabled"
	}
	return "disabled"
}

func kindName(synced bool) string {
	if synced {
		return "synced"
	}
	return "device-bound"
}

func shortID(id string) string {
	if len(id) > 16 {
		return id[:16]
	}
	return id
}

func revokedAt(t time.Time) string {
	if t.IsZero() {
		return "active"
	}
	return "revoked=" + day(t)
}

func day(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.UTC().Format("2006-01-02")
}
