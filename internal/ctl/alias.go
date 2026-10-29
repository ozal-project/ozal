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

// AliasRequest is one run of `ozalctl app alias`.
type AliasRequest struct {
	Name      string
	Target    Target
	From, To  string
	ManualDNS bool
	Wait      time.Duration
	Apply     bool
}

// AliasCommand runs `ozal app alias` on the edge as the service account, so
// the admin socket (the host operator's credential) is the only door and no
// token or DNS credential ever leaves the edge. The guard looks for the
// subcommand's usage text in the installed binary first: an older binary
// would not know "app" and would start as a second server instead.
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
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = Quote(a)
	}
	return fmt.Sprintf("grep -qaF 'ozal app alias' %[1]s || exit %[2]d; sudo -u %[3]s %[1]s %[4]s",
		Quote(e.Binary), ExitNoAlias, Quote(e.ServiceUser), strings.Join(quoted, " "))
}

// Alias gives the application served at From a second hostname and, with
// Apply, makes the edge create the DNS record itself. Without Apply it prints
// the edge's plan. It needs the operator's terminal for sudo.
func Alias(ctx context.Context, out io.Writer, remote Remote, r AliasRequest) error {
	e := r.Target.Edge
	if r.From == "" || r.To == "" {
		return errors.New("-from and -to are required")
	}
	if e.Socket == "" || e.Binary == "" || e.ServiceUser == "" {
		return errors.New("edge.socket, edge.binary and edge.service_user are required in the targets file")
	}
	fmt.Fprintf(out, "==> %s: %s -> %s%s\n", r.Name, r.From, r.To, map[bool]string{true: "", false: " (plan only)"}[r.Apply])
	code, err := remote.Terminal(ctx, AliasCommand(e, r))
	if err != nil {
		return fmt.Errorf("%w: run this from your own terminal so sudo can ask for the password", err)
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
