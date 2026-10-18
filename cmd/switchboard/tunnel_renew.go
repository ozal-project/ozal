package main

import (
	"context"
	"flag"
	"io"
	"os"

	"github.com/octaviosantacruz/switchboard/internal/tunnelrenew"
)

func runTunnelRenew(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("tunnel renew", flag.ContinueOnError)
	fs.SetOutput(out)
	force := fs.Bool("force", false, "renew both leaves now, including service restarts")
	initialize := fs.Bool("init", false, "initialize identity pointers without changing services")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return flag.ErrHelp
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return tunnelrenew.Run(ctx, tunnelrenew.Pilot{Home: home}, home, *force, *initialize, out)
}

func runTunnelRenewAgent(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("tunnel renew-agent", flag.ContinueOnError)
	fs.SetOutput(out)
	role := fs.String("role", "", "local identity role (edge or connector)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return flag.ErrHelp
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return tunnelrenew.ServeAgent(ctx, tunnelrenew.Agent{Home: home, Role: *role}, os.Stdin, out)
}
