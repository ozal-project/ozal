package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ozal-project/ozal/internal/ctl/artifact"
	"github.com/ozal-project/ozal/internal/ctl/publish"
)

func siteSnapshot(args []string, pack bool) int {
	fs := flag.NewFlagSet("site check", flag.ContinueOnError)
	dir := fs.String("dir", "", "build output directory")
	output := fs.String("out", "", "new archive file (site pack only)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *dir == "" || (pack && *output == "") {
		return finish(errors.New("-dir is required; site pack also needs -out"))
	}
	snapshot, err := artifact.FromDir(*dir)
	if err != nil {
		return finish(err)
	}
	if pack {
		if err := artifact.WriteNew(*output, snapshot.Archive); err != nil {
			return finish(err)
		}
	}
	fmt.Printf("Artifact checks passed: %d archive bytes; public proof %s.\n", len(snapshot.Archive), snapshot.Proof.Path)
	return 0
}

func githubReceipt(receipt publish.Receipt) error {
	if path := os.Getenv("GITHUB_OUTPUT"); path != "" {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return errors.New("cannot open GitHub output")
		}
		_, writeErr := fmt.Fprintf(file, "live=true\ndeployment_id=%s\n", receipt.ID)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return errors.New("cannot record GitHub output")
		}
	}
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return errors.New("cannot open GitHub summary")
		}
		_, writeErr := fmt.Fprintf(file, "Deployment `%s` is live.\n", receipt.ID[:12])
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return errors.New("cannot record GitHub summary")
		}
	}
	return nil
}

func siteDeploy(args []string) int {
	fs := flag.NewFlagSet("site deploy", flag.ContinueOnError)
	endpoint := fs.String("endpoint", os.Getenv("DEPLOY_ENDPOINT"), "HTTPS project deploy endpoint (default DEPLOY_ENDPOINT)")
	project := fs.String("project", os.Getenv("DEPLOY_PROJECT"), "project (default DEPLOY_PROJECT)")
	archive := fs.String("archive", "", "verified build archive")
	result := fs.String("result", "", "new private deployment receipt file")
	apply := fs.Bool("apply", false, "request Actions OIDC and upload (default: plan)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *archive == "" {
		return finish(errors.New("-archive is required"))
	}
	snapshot, err := artifact.ReadArchive(*archive)
	if err != nil {
		return finish(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	issuer := publish.GitHubTokenSource{URL: os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), Authorization: os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")}
	_, err = publish.CI(ctx, os.Stdout, issuer, publish.CIRequest{Endpoint: *endpoint, Project: *project, ResultFile: *result, Archive: snapshot.Archive, Apply: *apply, Confirmed: githubReceipt})
	return finish(err)
}

func siteAccept(args []string) int {
	fs := flag.NewFlagSet("site accept", flag.ContinueOnError)
	address := fs.String("url", os.Getenv("SITE_URL"), "public http(s) origin")
	plain := fs.String("http-url", "", "alternate plain HTTP origin (default: same host)")
	dir := fs.String("dir", "", "build output directory to compare")
	attempts := fs.Int("attempts", 10, "public build checks before refusing")
	wait := fs.Duration("wait", 3*time.Second, "delay between public checks")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *dir == "" || *attempts < 1 || *attempts > 100 || *wait < 0 || *wait > time.Minute {
		return finish(errors.New("valid -dir, attempts (1-100) and wait (0-1m) are required"))
	}
	snapshot, err := artifact.FromDir(*dir)
	if err != nil {
		return finish(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return finish(publish.Accept(ctx, os.Stdout, publish.Acceptance{URL: *address, HTTPURL: *plain, Proof: snapshot.Proof, Attempts: *attempts, Wait: *wait}))
}
