package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ozal-project/ozal/internal/tunnelrenew"
)

func runTunnelAuthority(ctx context.Context, args []string, out io.Writer) error {
	if len(args) < 2 {
		return errors.New("usage: ozal tunnel ca init | ozal tunnel identity prepare|sign|check [flags]")
	}
	mode, action := args[0], args[1]
	fs := flag.NewFlagSet("tunnel "+mode+" "+action, flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("dir", "", "new private identity/CA directory")
	role := fs.String("role", "edge", "fixed tunnel role: edge or connector")
	ca := fs.String("ca", "", "CA directory for signing; public CA file for checking")
	csr := fs.String("csr", "", "public CSR file")
	certificate := fs.String("out", "", "new public certificate file")
	days := fs.Int("days", 366, "new tunnel CA lifetime (38-3650 days)")
	apply := fs.Bool("apply", false, "write new files (default: plan)")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected tunnel authority arguments")
	}
	if mode == "ca" && action == "init" {
		if *dir == "" || *days < 38 || *days > 3650 {
			return errors.New("a new -dir and lifetime of 38-3650 days are required")
		}
		if _, err := os.Lstat(*dir); !os.IsNotExist(err) {
			return errors.New("CA directory already exists or cannot be inspected")
		}
		fmt.Fprintf(out, "Create a new private tunnel CA (%d days, apply=%t); no trust is deployed.\n", *days, *apply)
		if !*apply {
			return nil
		}
		return tunnelrenew.CreateAuthority(ctx, *dir, *days, time.Now())
	}
	if mode != "identity" || (*role != "edge" && *role != "connector") {
		return errors.New("identity requires a fixed edge or connector role")
	}
	switch action {
	case "prepare":
		if *dir == "" {
			return errors.New("prepare requires a new -dir")
		}
		fmt.Fprintf(out, "Prepare %s key and public CSR locally (apply=%t).\n", *role, *apply)
		if !*apply {
			return nil
		}
		return tunnelrenew.PrepareRole(ctx, *role, *dir)
	case "sign":
		if *ca == "" || *csr == "" || *certificate == "" {
			return errors.New("sign requires -ca DIRECTORY -csr FILE -out NEW_FILE")
		}
		fmt.Fprintf(out, "Sign public CSR for fixed %s role (apply=%t).\n", *role, *apply)
		if !*apply {
			return nil
		}
		return tunnelrenew.SignRole(ctx, *role, *ca, *csr, *certificate)
	case "check":
		if *dir == "" || *ca == "" {
			return errors.New("check requires -dir IDENTITY_DIRECTORY -ca PUBLIC_CA_FILE")
		}
		if err := tunnelrenew.CheckRole(ctx, *role, *dir, *ca); err != nil {
			return err
		}
		fmt.Fprintln(out, "Role, local key, CA and lifetime checks passed; access still requires enrollment.")
		return nil
	default:
		return errors.New("identity action must be prepare, sign or check")
	}
}
