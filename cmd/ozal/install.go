package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"

	"github.com/ozal-project/ozal/internal/install"
)

func runInstall(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "binary" && args[0] != "service") {
		return errors.New("usage: ozal install binary -archive FILE -sha256 HEX -destination PATH [-apply] | ozal install service [flags]")
	}
	fs := flag.NewFlagSet("install "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	archive := fs.String("archive", "", "release archive verified against -sha256")
	checksum := fs.String("sha256", "", "trusted release SHA-256")
	apply := fs.Bool("apply", false, "install (default: plan only)")
	if args[0] == "binary" {
		destination := fs.String("destination", "", "new binary path, never overwritten")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("unexpected install arguments")
		}
		return install.Binary(out, install.BinaryRequest{Archive: *archive, SHA256: *checksum, Destination: *destination, Apply: *apply})
	}
	binary := fs.String("binary", "", "verified Linux binary (default: this executable)")
	root := fs.String("root", "", "stage files under an absolute directory; no account or service changes")
	target := fs.String("target", "http://127.0.0.1:3000", "existing application URL")
	group := fs.String("group", "members", "first application's required group")
	unit := fs.Bool("print-unit", false, "print the embedded unit and stop")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected install arguments")
	}
	if *unit {
		_, err := io.WriteString(out, install.Unit())
		return err
	}
	if *binary == "" && *archive == "" {
		var err error
		*binary, err = os.Executable()
		if err != nil {
			return err
		}
	}
	return install.Service(ctx, out, install.LinuxHost{}, install.ServiceRequest{Binary: *binary, Archive: *archive, SHA256: *checksum, Root: *root, Target: *target, Group: *group, Apply: *apply})
}
