package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"

	"github.com/switchboard-project/switchboard/internal/config"
)

func runGitHub(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "connect" && args[0] != "disconnect") {
		return errors.New("usage: switchboard github connect|disconnect [flags]")
	}
	fs := flag.NewFlagSet("github "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	af := addAdminFlags(fs)
	appID := fs.Int64("app-id", 0, "GitHub App ID")
	installation := fs.Int64("installation-id", 0, "selected-repository installation ID")
	key := fs.String("key", "", "private key reference, file:/absolute/path or env:NAME (never the key itself)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected GitHub arguments")
	}
	var app *config.GitHubApp
	if args[0] == "connect" {
		app = &config.GitHubApp{AppID: *appID, InstallationID: *installation, PrivateKey: *key}
		if err := app.Validate(); err != nil {
			return err
		}
	}
	client, err := af.client()
	if err != nil {
		return err
	}
	var status struct {
		Expected string `json:"config_sha256"`
	}
	if err := client.do(ctx, http.MethodGet, "/_/api/setup", nil, &status); err != nil {
		return err
	}
	if err := client.do(ctx, http.MethodPut, "/_/api/github", map[string]any{"app": app, "expected": status.Expected}, nil); err != nil {
		return err
	}
	if app == nil {
		fmt.Fprintln(out, "Repository discovery disconnected. Existing per-project deploy trust remains; remove each publisher in CI projects to stop future deployments.")
	} else {
		fmt.Fprintln(out, "GitHub App reference saved. Open CI projects to verify selected repositories and review publisher trust. No private key was sent to GitHub or the browser by this command.")
	}
	return nil
}
