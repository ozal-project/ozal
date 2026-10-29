package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
	cf "github.com/ozal-project/ozal/internal/cloudflare"
	"github.com/ozal-project/ozal/internal/ctl"
	"github.com/ozal-project/ozal/internal/ctl/sshexec"
)

func cloudflareOperation(args []string, kind string) int {
	fs := flag.NewFlagSet("cloudflare "+kind, flag.ContinueOnError)
	host := fs.String("host", "", "hostname to manage")
	like := fs.String("like", "", "reference hostname for Origin Pulls")
	token := fs.String("token-file", "", "private Cloudflare token file")
	backup := fs.String("backup", "", "new private metadata backup")
	apply := fs.Bool("apply", false, "apply scoped change after backup (default: plan)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *token == "" {
		return finish(errors.New("-token-file and -host are required"))
	}
	hostname, err := cf.Hostname(*host)
	if err != nil {
		return finish(err)
	}
	credential, err := sshexec.ReadToken(*token)
	if err != nil {
		return finish(errors.New("cannot read private Cloudflare token file"))
	}
	client, err := certs.NewCloudflareClient(certs.CloudflareConfig{Token: credential})
	if err != nil {
		return finish(err)
	}
	if *backup == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return finish(err)
		}
		*backup = filepath.Join(home, ".local/state/ozalctl", fmt.Sprintf("cloudflare-%s-%s-%d.json", kind, hostname, time.Now().UnixNano()))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 2*time.Minute)
	defer timeout()
	return finish(ctl.Cloudflare(ctx, os.Stdout, client, ctl.CloudflareRequest{Kind: kind, Host: hostname, Like: *like, Backup: *backup, Apply: *apply}))
}
