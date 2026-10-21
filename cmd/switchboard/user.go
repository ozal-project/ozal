package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/passkey"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

const (
	defaultInviteTTL = 15 * time.Minute
	maxInviteTTL     = 24 * time.Hour
)

// runUser implements `switchboard user invite|list|passkeys|revoke|disable|enable`:
// the operator's side of passkey login (ID-003). It runs on the host
// against the state database; the shell is the root of trust, so this is
// also the only way to recover an account.
func runUser(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard user invite|list|passkeys|revoke|disable|enable [flags]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("user "+sub, flag.ContinueOnError)
	fs.SetOutput(out)
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	configPath := fs.String("config", "config.json", "routing config with auth.passkeys (invite reads login_url from it)")
	email := fs.String("email", "", "the user's email address")
	name := fs.String("name", "", "display name shown by the authenticator (invite)")
	recoverAccess := fs.Bool("recover", false, "invite a user who already has a passkey to add another (lost device)")
	ttl := fs.Duration("ttl", defaultInviteTTL, "how long the enrolment link stays valid (at most 24h)")
	prefix := fs.String("passkey", "", "leading hex characters of the passkey ID (revoke)")
	var groups []string
	fs.Func("group", "group the user belongs to, for access policies (repeatable)", func(v string) error {
		v = strings.TrimSpace(v)
		if v == "" {
			return errors.New("empty group")
		}
		groups = append(groups, v)
		return nil
	})
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	needsEmail := sub != "list"
	if needsEmail && *email == "" {
		return fmt.Errorf("%s needs -email", sub)
	}

	db, err := openMigratedDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	identity := sqlite.NewIdentity(db)

	switch sub {
	case "invite":
		if *ttl <= 0 || *ttl > maxInviteTTL {
			return fmt.Errorf("-ttl must be between 1s and %s", maxInviteTTL)
		}
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		if cfg.Auth.Passkeys == nil {
			return fmt.Errorf("%s has no auth.passkeys section", *configPath)
		}
		invitation, err := identity.Invite(ctx, *email, *name, groups, *recoverAccess, *ttl)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Enrolment link for %s (single use, expires %s):\n\n  %s/_/auth/enrol#%s\n\n",
			invitation.User.Email, invitation.ExpiresAt.UTC().Format(time.RFC3339), cfg.Auth.Passkeys.LoginURL, invitation.Token)
		fmt.Fprintln(out, "Send it over a channel you trust: whoever opens it first gets the passkey.")
	case "list":
		users, err := identity.Users(ctx)
		if err != nil {
			return err
		}
		for _, u := range users {
			state := "enabled"
			if !u.DisabledAt.IsZero() {
				state = "disabled"
			}
			fmt.Fprintf(out, "%s\t%s\t%s\tgroups=%s\n", u.Email, u.Subject(), state, strings.Join(u.Groups, ","))
		}
	case "passkeys":
		keys, err := identity.Passkeys(ctx, *email)
		if err != nil {
			return userError(err, *email)
		}
		for _, k := range keys {
			fmt.Fprintf(out, "%s\t%s\tcreated=%s\tlast_used=%s\t%s\n",
				hex.EncodeToString(k.Credential.ID)[:16], kindOf(k), day(k.CreatedAt), day(k.LastUsedAt), revokedState(k))
		}
	case "revoke":
		if err := identity.RevokePasskey(ctx, *email, *prefix); err != nil {
			return userError(err, *email)
		}
		fmt.Fprintf(out, "revoked passkey %s of %s; the user's sessions are closed\n", *prefix, *email)
	case "disable", "enable":
		if err := identity.SetDisabled(ctx, *email, sub == "disable"); err != nil {
			return userError(err, *email)
		}
		if sub == "disable" {
			fmt.Fprintf(out, "disabled %s; the user's sessions are closed\n", *email)
		} else {
			fmt.Fprintf(out, "enabled %s\n", *email)
		}
	default:
		return fmt.Errorf("unknown user command %q", sub)
	}
	return nil
}

func userError(err error, email string) error {
	if errors.Is(err, passkey.ErrNotFound) {
		return fmt.Errorf("no user %s", email)
	}
	return err
}

func kindOf(k passkey.Passkey) string {
	if k.Synced() {
		return "synced"
	}
	return "device-bound"
}

func revokedState(k passkey.Passkey) string {
	if k.RevokedAt.IsZero() {
		return "active"
	}
	return "revoked=" + day(k.RevokedAt)
}

func day(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.UTC().Format("2006-01-02")
}
