package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/upgrade"
)

// upgradeExit carries the upgrade's result to main as an exit status:
// 2 refused (unit untouched), 3 rolled back, 4 down.
type upgradeExit struct {
	code int
	err  error
}

func (e *upgradeExit) Error() string { return e.err.Error() }

// runUpgrade implements `switchboard upgrade` (REL-010): run the NEW binary
// as root to replace the one a systemd unit runs. See internal/upgrade.
func runUpgrade(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(out)
	unit := fs.String("unit", "", "systemd unit running the installed binary (required)")
	binary := fs.String("binary", "", "installed binary to replace, absolute (required)")
	sum := fs.String("sha256", "", "expected SHA-256 of this binary, from where it was built (required)")
	configPath := fs.String("config", "", "routing config the unit runs with (required)")
	dbPath := fs.String("db", "", "state database the unit runs with (empty: none)")
	account := fs.String("user", "", "service account that owns the state (required with -db)")
	keep := fs.Int("keep", 3, "previous binaries to keep beside -binary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *unit == "" || *binary == "" || *sum == "" || *configPath == "" {
		return errors.New("usage: switchboard upgrade -unit UNIT -binary PATH -sha256 HEX -config PATH [-db PATH -user ACCOUNT] [-keep 3]")
	}
	if !filepath.IsAbs(*binary) || (*dbPath != "" && !filepath.IsAbs(*dbPath)) {
		return errors.New("-binary and -db must be absolute paths")
	}
	if *dbPath != "" && *account == "" {
		return errors.New("-db needs -user: the state must stay the service account's")
	}
	if *keep < 1 {
		return errors.New("-keep must be at least 1")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	source, err := os.Executable()
	if err == nil {
		source, err = filepath.EvalSymlinks(source)
	}
	if err != nil {
		return fmt.Errorf("locate this binary: %w", err)
	}
	sys, err := newUpgradeSystem(*unit, *account, "http://"+cfg.Admin.Listen+"/_/status", out)
	if err != nil {
		return err
	}
	// The log beside the binary survives a dropped session.
	logFile, err := os.OpenFile(*binary+".upgrade.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	// The file first: a write to a terminal that went away fails, and
	// MultiWriter stops at the first failing writer.
	w := io.MultiWriter(logFile, out)

	plan := upgrade.Plan{
		Unit: *unit, Binary: *binary, Source: source, SHA256: *sum,
		Config: *configPath, DB: *dbPath, Keep: *keep,
	}
	res, err := upgrade.Run(ctx, plan, sys, w)
	if res == upgrade.Upgraded {
		return nil
	}
	return &upgradeExit{code: res.ExitCode(), err: err}
}
