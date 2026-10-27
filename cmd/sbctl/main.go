// Command sbctl is the operator's tool for shipping Switchboard to edges. It
// runs on the operator's machine and talks to servers over SSH; it is never
// installed on a server, and the edge binary does not contain it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/ctl"
	"github.com/octaviosantacruz/switchboard/internal/ctl/sshexec"
)

const usage = `usage: sbctl edge upgrade [-targets FILE] [-target NAME] [-repo DIR] [-ref REF] [-apply]

Builds a committed ref of Switchboard, uploads it to the edge and runs
"switchboard upgrade" there. Without -apply it only prints the plan.
The targets file lists one entry per server (see sbctl.example.json).
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 2 || args[0] != "edge" || args[1] != "upgrade" {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("edge upgrade", flag.ContinueOnError)
	home, _ := os.UserHomeDir()
	targetsPath := fs.String("targets", defaultTargets(home), "targets file")
	name := fs.String("target", "", "target name (optional when the file has one)")
	repo := fs.String("repo", ".", "Switchboard checkout to build from")
	ref := fs.String("ref", "HEAD", "committed ref to build")
	apply := fs.Bool("apply", false, "do it (default: print the plan)")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}

	file, err := os.Open(*targetsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sbctl:", err)
		return 2
	}
	targets, err := ctl.ParseTargets(file)
	file.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sbctl:", err)
		return 2
	}
	targetName, target, err := targets.Pick(*name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sbctl:", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	host, _ := os.Hostname()
	req := ctl.UpgradeRequest{
		Name: targetName, Target: target, Ref: *ref, Apply: *apply,
		Owner: fmt.Sprintf("sbctl on %s pid %d since %s", host, os.Getpid(), time.Now().UTC().Format(time.RFC3339)),
	}
	remote := sshexec.Remote{Target: target.SSH.Target, Key: target.SSH.Key, Interactive: isTerminal(os.Stdin)}
	builder := sshexec.GitBuilder{
		Repo: *repo, GOOS: "linux", GOARCH: "amd64",
		WorkDir: filepath.Join(home, ".cache", "sbctl"),
	}
	if err := ctl.Upgrade(ctx, os.Stdout, remote, builder, req); err != nil {
		fmt.Fprintln(os.Stderr, "sbctl:", err)
		return 1
	}
	return 0
}

func defaultTargets(home string) string {
	if _, err := os.Stat("sbctl.json"); err == nil {
		return "sbctl.json"
	}
	return filepath.Join(home, ".config", "sbctl", "targets.json")
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
