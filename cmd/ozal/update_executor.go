package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ozal-project/ozal/internal/updateexec"
	"github.com/ozal-project/ozal/internal/updatejob"
)

// runUpdateExecutor is the local, separately supervised installation
// executor (REL-019/REL-020). It reads every path and unit from a root-owned
// profile; the console only asks it to check the signed catalog or to apply
// the plan it returned.
func runUpdateExecutor(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("update-executor", flag.ContinueOnError)
	fs.SetOutput(out)
	profilePath := fs.String("profile", "", "root-owned executor profile (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *profilePath == "" {
		return errors.New("usage: ozal update-executor -profile FILE")
	}
	profile, err := updateexec.LoadProfile(*profilePath)
	if err != nil {
		return err
	}
	runner := &installedUpdateRunner{binary: profile.Binary}
	server := &updateexec.Server{
		Profile: profile,
		Store:   updatejob.Store{Dir: profile.Jobs},
		Runner:  runner,
	}
	fmt.Fprintf(out, "update executor: listening on %s\n", profile.Socket)
	return server.Serve(ctx)
}

// installedUpdateRunner runs the installed binary's own update command. The
// profile's binary path is fixed; no request can choose another executable.
type installedUpdateRunner struct {
	binary string
}

func (r installedUpdateRunner) Run(ctx context.Context, args []string, log io.Writer) error {
	cmd := exec.CommandContext(ctx, r.binary, append([]string{"update"}, args...)...)
	cmd.Stdout, cmd.Stderr = log, log
	// The candidate is executed before installation, so staging must be on an
	// executable filesystem. The profile has already validated this root-owned
	// binary directory; the host's /tmp may be mounted noexec.
	cmd.Env = append(os.Environ(), "TMPDIR="+filepath.Dir(r.binary))
	return cmd.Run()
}
