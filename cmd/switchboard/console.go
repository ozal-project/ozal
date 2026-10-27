package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func runConsole(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard console enrol|recover|login|list|revoke [flags]")
	}
	fs := flag.NewFlagSet("console "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	af := addAdminFlags(fs)
	name := fs.String("name", "", "operator name asserted by host authority")
	tools := fs.String("tools", "", "comma-separated tools this login may change (default: all): applications, connectors, people, publishing, sites")
	email := fs.String("email", "", "recover: email of the administrator who lost their passkeys")
	id := fs.String("id", "", "session id to revoke")
	origin := fs.String("url", "", "private browser URL (default: the admin.listen port of the config; use the locally forwarded port when it differs)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected console arguments")
	}
	c, err := af.client()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(*af.target, "unix:") && c.base != "http://localhost" {
		return errors.New("console credentials are issued on the host admin Unix socket only")
	}
	if *origin == "" {
		*origin = forwardedURL(af.adminListen())
	}
	switch args[0] {
	case "enrol", "recover":
		kind := "bootstrap"
		if args[0] == "recover" {
			kind = "recover"
			if strings.TrimSpace(*email) == "" {
				return errors.New("recover needs -email ADMINISTRATOR")
			}
		}
		base, err := adminBase(*origin)
		if err != nil {
			return err
		}
		var answer struct {
			Token string `json:"token"`
		}
		if err := c.do(ctx, http.MethodPost, "/_/api/admin/enrolments", map[string]string{"kind": kind, "email": *email}, &answer); err != nil {
			return err
		}
		what := "create the first administrator"
		if kind == "recover" {
			what = "replace the passkeys of " + *email + " (the old ones stop working)"
		}
		fmt.Fprintf(out, "Open once within ten minutes in your browser to %s. Passkeys need the name localhost, so the link uses it:\n%s/overview#enrol=%s\n", what, passkeyOrigin(base), url.QueryEscape(answer.Token))
		fmt.Fprint(out, sshHint(os.Getenv("SSH_CONNECTION"), invokingUser(), base))
	case "login":
		if strings.TrimSpace(*name) == "" {
			return errors.New("login needs -name OPERATOR")
		}
		base, err := adminBase(*origin)
		if err != nil {
			return err
		}
		var answer struct {
			Token   string    `json:"token"`
			Expires time.Time `json:"expires"`
		}
		if err := c.do(ctx, http.MethodPost, "/_/api/operators", map[string]any{"name": *name, "tools": splitTools(*tools)}, &answer); err != nil {
			return err
		}
		fmt.Fprintf(out, "Open once within five minutes on the private console (do not share this link):\n%s/overview#operator=%s\n", base, url.QueryEscape(answer.Token))
		fmt.Fprint(out, sshHint(os.Getenv("SSH_CONNECTION"), invokingUser(), base))
	case "list":
		var answer struct {
			Sessions []struct {
				ID      string    `json:"id"`
				Name    string    `json:"name"`
				Expires time.Time `json:"expires"`
			} `json:"sessions"`
		}
		if err := c.do(ctx, http.MethodGet, "/_/api/operators", nil, &answer); err != nil {
			return err
		}
		for _, session := range answer.Sessions {
			fmt.Fprintf(out, "%s\t%s\t%s\n", session.ID, session.Name, session.Expires.Format(time.RFC3339))
		}
	case "revoke":
		if *id == "" {
			return errors.New("revoke needs -id ID")
		}
		if err := c.do(ctx, http.MethodDelete, "/_/api/operators/"+url.PathEscape(*id), nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "Operator session revoked")
	default:
		return errors.New("unknown console command")
	}
	return nil
}

// splitTools turns "people, sites" into a list; the edge validates the names.
func splitTools(v string) []string {
	out := []string{}
	for _, t := range strings.Split(v, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// passkeyOrigin rewrites a loopback address to the name localhost: WebAuthn
// binds a passkey to a domain name and refuses an IP address.
func passkeyOrigin(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
		if port := u.Port(); port != "" {
			u.Host = "localhost:" + port
		} else {
			u.Host = "localhost"
		}
	}
	return u.String()
}

// forwardedURL is the browser URL for an admin.listen value such as
// "127.0.0.1:9091"; an unset or unreadable value keeps the historic port.
func forwardedURL(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "http://127.0.0.1:9090"
	}
	return "http://127.0.0.1:" + port
}

// invokingUser is the person at the keyboard, not root under sudo.
func invokingUser() string {
	if u := os.Getenv("SUDO_USER"); u != "" {
		return u
	}
	return os.Getenv("USER")
}

// sshHint tells someone who is logged into the host over SSH how to reach the
// admin console from their own computer: the console only listens on the
// host's loopback, so their browser needs a forwarded port. It is empty when
// the command is not running in an SSH session.
func sshHint(sshConnection, user, base string) string {
	fields := strings.Fields(sshConnection)
	u, err := url.Parse(base)
	if len(fields) != 4 || err != nil || u.Port() == "" || user == "" {
		return ""
	}
	return fmt.Sprintf("From your computer, forward the port first, then open the link:\n  ssh -N -L %[1]s:127.0.0.1:%[1]s %[2]s@%[3]s\n", u.Port(), user, fields[2])
}

// enrolCommand is the command to run on this host to create the first
// administrator: short when `switchboard` on the PATH is this very binary and
// the config is at the installed path, explicit otherwise. lookPath resolves
// a command name to the file it runs ("" when absent).
func enrolCommand(exe, configPath string, lookPath func(string) string) string {
	name := exe
	if found := lookPath("switchboard"); found != "" && found == exe {
		name = "switchboard"
	}
	command := "sudo " + name + " console enrol"
	if configPath != installedConfig {
		command += " -config " + configPath
	}
	return command
}
