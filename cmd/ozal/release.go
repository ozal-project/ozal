package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/release"
)

// runRelease dispatches `ozal release keygen|sign`: the two steps a project
// needs to publish releases that edges update to on their own.
func runRelease(_ context.Context, args []string, out io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "keygen":
			return runReleaseKeygen(args[1:], out)
		case "sign":
			return runReleaseSign(args[1:], out)
		}
	}
	return errors.New("usage: ozal release keygen -key FILE | ozal release sign -key REF -in checksums.txt -out checksums.txt.sig")
}

// runReleaseKeygen writes a new signing key and prints the public key edges
// pin. The private key file is created, never replaced: losing the old key by
// accident would strand every edge that pins it.
func runReleaseKeygen(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("release keygen", flag.ContinueOnError)
	fs.SetOutput(out)
	path := fs.String("key", "", "file to create with the private signing key (mode 0600)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || fs.NArg() != 0 {
		return errors.New("usage: ozal release keygen -key FILE")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", *path, err)
	}
	if _, err := fmt.Fprintln(f, base64.StdEncoding.EncodeToString(priv.Seed())); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(out, "signing key written to %s: store it as the OZAL_RELEASE_KEY secret of the release workflow, then delete this file\n", *path)
	fmt.Fprintf(out, "public key (pin it on every edge, e.g. /etc/ozal/release.pub):\n%s\n", base64.StdEncoding.EncodeToString(pub))
	return nil
}

// runReleaseSign signs a checksums file. The key is an env: or file:
// reference, so CI never puts it on a command line.
func runReleaseSign(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("release sign", flag.ContinueOnError)
	fs.SetOutput(out)
	keyRef := fs.String("key", "env:OZAL_RELEASE_KEY", "env: or file: reference to the private signing key")
	in := fs.String("in", release.ChecksumsName, "checksums file to sign")
	outPath := fs.String("out", release.SignatureName, "signature file to write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if !strings.HasPrefix(*keyRef, "env:") && !strings.HasPrefix(*keyRef, "file:") {
		return errors.New("-key must be an env: or file: reference; a key on the command line ends up in shell history and process lists")
	}
	text, err := config.ResolveSecret(*keyRef)
	if err != nil {
		return fmt.Errorf("signing key: %w", err)
	}
	key, err := release.ParsePrivateKey(text)
	if err != nil {
		return err
	}
	checksums, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, []byte(release.Sign(checksums, key)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "signed %s -> %s\n", *in, *outPath)
	return nil
}
