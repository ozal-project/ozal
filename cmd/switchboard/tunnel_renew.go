package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	"github.com/switchboard-project/switchboard/internal/tunnelrenew"
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
	dir := fs.String("dir", "", "system layout: the role's identities and state (with -config and -unit)")
	configPath := fs.String("config", "", "system layout: the edge's routing config")
	unit := fs.String("unit", "", "system layout: the system unit, restarted with `sudo -n systemctl restart`")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return flag.ErrHelp
	}
	agent := tunnelrenew.Agent{Role: *role}
	if *dir != "" || *configPath != "" || *unit != "" {
		// Postura 2: run by the role's own account from a root-owned binary.
		if !filepath.IsAbs(*dir) || !filepath.IsAbs(*configPath) || *unit == "" {
			return errors.New("-dir, -config (absolute) and -unit go together")
		}
		binary, err := os.Executable()
		if err == nil {
			binary, err = filepath.EvalSymlinks(binary)
		}
		if err != nil {
			return err
		}
		agent.Dir, agent.Config, agent.Binary, agent.Unit = *dir, *configPath, binary, *unit
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		agent.Home = home
	}
	return tunnelrenew.ServeAgent(ctx, agent, os.Stdin, out)
}
