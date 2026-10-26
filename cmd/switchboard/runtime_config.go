package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"io"
	"os"
)

// runRuntime is an explicit offline bootstrap, like migrate: a missing
// authoritative operational file must never silently restore older root policy.
func runRuntime(_ context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "init" {
		return errors.New("usage: switchboard runtime init -config ROOT_CONFIG")
	}
	fs := flag.NewFlagSet("runtime init", flag.ContinueOnError)
	fs.SetOutput(out)
	path := fs.String("config", "config.json", "root host config that declares runtime_file")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected runtime arguments")
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		return errors.New("cannot read root config for explicit initialization")
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		return errors.New("root config is invalid; inspect it locally")
	}
	if cfg.RuntimeFile == "" {
		return errors.New("root config must declare runtime_file")
	}
	if err := config.ValidateOperatingPath(*path, cfg.RuntimeFile); err != nil {
		return err
	}
	data, err := config.OperatingJSON(*cfg)
	if err != nil {
		return err
	}
	if err := writeNewPrivateFile(cfg.RuntimeFile, data); err != nil {
		return errors.New("runtime file must be new and writable; an existing policy is never overwritten")
	}
	fmt.Fprintln(out, "Initialized operational policy from the root seed. Existing runtime policy is never overwritten; future startup/reload fails closed if this file is missing or invalid.")
	return nil
}
