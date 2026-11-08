package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	"github.com/ozal-project/ozal/internal/assertion"
	"github.com/ozal-project/ozal/internal/config"
)

// runAssertion prints the public key applications pin to verify this edge's
// identity assertions. It never prints the private key.
func runAssertion(_ context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "public-key" {
		return errors.New("usage: ozal assertion public-key -config CONFIG")
	}
	fs := flag.NewFlagSet("assertion public-key", flag.ContinueOnError)
	fs.SetOutput(out)
	path := fs.String("config", "config.json", "host config with identity_assertion.key_dir")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected assertion arguments")
	}
	cfg, err := config.Load(resolveConfigPath(*path))
	if err != nil {
		return err
	}
	if cfg.IdentityAssertion == nil {
		return errors.New("the config has no identity_assertion.key_dir")
	}
	dir := cfg.IdentityAssertion.KeyDir
	if _, err := os.Stat(filepath.Join(dir, assertion.KeyFile)); errors.Is(err, os.ErrNotExist) && os.Geteuid() == 0 {
		// A key created by root would be unreadable for the service account.
		return errors.New("no signing key yet: start Ozal once, or run this as the service account (sudo -u ozal ozal assertion public-key)")
	}
	key, err := assertion.LoadOrCreateKey(dir)
	if err != nil {
		return err
	}
	signer, err := assertion.NewSigner(key)
	if err != nil {
		return err
	}
	_, err = out.Write(signer.PublicKeyPEM())
	return err
}
