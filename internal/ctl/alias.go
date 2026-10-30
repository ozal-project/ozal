package ctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ExitNoAlias is the status of AliasCommand's guard: the edge's binary
// predates `ozal app alias`.
const ExitNoAlias = 3

// ExitNoSocketAccess is the status of the socket_group guard: the SSH user
// cannot open the edge's admin socket.
const ExitNoSocketAccess = 4

// socketCommand runs the edge binary against its admin socket after the
// guard that it knows the subcommand. With edge.socket_group the SSH user
// opens the socket itself (no sudo); otherwise it runs as the service
// account through sudo.
func socketCommand(e Edge, usage string, missing int, args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = Quote(a)
	}
	guard := fmt.Sprintf("grep -qaF %s %s || exit %d; ", Quote(usage), Quote(e.Binary), missing)
	if e.SocketGroup != "" {
		return guard + fmt.Sprintf("test -r %[1]s -a -w %[1]s || exit %[2]d; %[3]s %[4]s",
			Quote(e.Socket), ExitNoSocketAccess, Quote(e.Binary), strings.Join(quoted, " "))
	}
	return guard + fmt.Sprintf("sudo -u %s %s %s", Quote(e.ServiceUser), Quote(e.Binary), strings.Join(quoted, " "))
}

// runOnSocket runs a socketCommand: without a terminal when the SSH user is
// in the socket group, with one (for sudo's prompt) otherwise.
func runOnSocket(ctx context.Context, remote Remote, e Edge, command string) (int, error) {
	if e.SocketGroup != "" {
		code, err := remote.Stream(ctx, command)
		if err == nil && code == ExitNoSocketAccess {
			return code, fmt.Errorf("the SSH user cannot open %s: add it to the edge's admin.socket_group %q (usermod -aG %s USER) and log in again; nothing changed", e.Socket, e.SocketGroup, e.SocketGroup)
		}
		return code, err
	}
	code, err := remote.Terminal(ctx, command)
	if err != nil {
		return code, fmt.Errorf("%w: run this from your own terminal so sudo can ask for the password, or set edge.socket_group", err)
	}
	return code, nil
}

// AliasRequest is one run of `ozalctl app alias`.
type AliasRequest struct {
	Name      string
	Target    Target
	From, To  string
	ManualDNS bool
	Wait      time.Duration
	Apply     bool
}

// AliasCommand runs `ozal app alias` on the edge against the admin socket
// (the host operator's credential), so no token or DNS credential ever
// leaves the edge. The guard looks for the subcommand's usage text in the
// installed binary first: an older binary would not know "app" and would
// start as a second server instead.
func AliasCommand(e Edge, r AliasRequest) string {
	args := []string{"app", "alias", "-admin", "unix:" + e.Socket, "-from", r.From, "-to", r.To}
	if r.ManualDNS {
		args = append(args, "-manual-dns")
	}
	if r.Wait > 0 {
		args = append(args, "-wait", r.Wait.String())
	}
	if r.Apply {
		args = append(args, "-apply")
	}
	return socketCommand(e, "ozal app alias", ExitNoAlias, args)
}

// Alias gives the application served at From a second hostname and, with
// Apply, makes the edge create the DNS record itself. Without Apply it prints
// the edge's plan. Without edge.socket_group it needs the operator's
// terminal for sudo.
func Alias(ctx context.Context, out io.Writer, remote Remote, r AliasRequest) error {
	e := r.Target.Edge
	if r.From == "" || r.To == "" {
		return errors.New("-from and -to are required")
	}
	if e.Socket == "" || e.Binary == "" || e.ServiceUser == "" {
		return errors.New("edge.socket, edge.binary and edge.service_user are required in the targets file")
	}
	fmt.Fprintf(out, "==> %s: %s -> %s%s\n", r.Name, r.From, r.To, map[bool]string{true: "", false: " (plan only)"}[r.Apply])
	code, err := runOnSocket(ctx, remote, e, AliasCommand(e, r))
	if err != nil {
		return err
	}
	switch code {
	case 0:
		return nil
	case ExitNoAlias:
		return errors.New("the edge's binary has no `app alias` yet: run `ozalctl edge upgrade -apply` first (nothing was changed)")
	default:
		return fmt.Errorf("app alias ended with status %d (255: SSH failed); its output above says what was kept", code)
	}
}
