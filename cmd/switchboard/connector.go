package main

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/tunnel"
)

// runConnector implements `switchboard connector enroll|rotate|current|revoke|list`: manage
// which tunnel connector certificates the edge accepts. The edge notices a
// revocation within its revalidation interval, so no restart is needed.
func runConnector(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard connector enroll|rotate|current|revoke|list [flags]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("connector "+sub, flag.ContinueOnError)
	fs.SetOutput(out)
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	name := fs.String("name", "", "connector name (a-z, 0-9, '-')")
	certPath := fs.String("cert", "", "connector certificate PEM (the public certificate only)")
	expected := fs.String("expected", "", "exact current certificate SHA-256 for atomic rotation")
	if err := fs.Parse(rest); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	db, err := openMigratedDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	registry := tunnel.NewRegistry(db)

	switch sub {
	case "current":
		if *name == "" {
			return errors.New("current needs -name")
		}
		fp, err := registry.Current(ctx, *name)
		if errors.Is(err, tunnel.ErrNotFound) {
			fmt.Fprintln(out, "none")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(out, hex.EncodeToString(fp[:]))
	case "rotate":
		raw, err := hex.DecodeString(*expected)
		if err != nil || len(raw) != 32 || *name == "" || *certPath == "" {
			return errors.New("rotate needs -name, -cert and -expected (64 hex characters)")
		}
		var previous tunnel.Fingerprint
		copy(previous[:], raw)
		next, err := fingerprintFromPEM(*certPath)
		if err != nil {
			return err
		}
		if err := registry.Rotate(ctx, *name, previous, next); err != nil {
			return err
		}
		fmt.Fprintf(out, "rotated %s (sha256:%s)\n", *name, hex.EncodeToString(next[:8]))
	case "enroll":
		if *name == "" || *certPath == "" {
			return errors.New("enroll needs -name and -cert")
		}
		fp, err := fingerprintFromPEM(*certPath)
		if err != nil {
			return err
		}
		if err := registry.Enroll(ctx, *name, fp); err != nil {
			return err
		}
		fmt.Fprintf(out, "enrolled %s (sha256:%s)\n", *name, hex.EncodeToString(fp[:8]))
	case "revoke":
		if *name == "" {
			return errors.New("revoke needs -name")
		}
		if err := registry.Revoke(ctx, *name); err != nil {
			return err
		}
		fmt.Fprintf(out, "revoked %s\n", *name)
	case "list":
		records, err := registry.List(ctx)
		if err != nil {
			return err
		}
		for _, r := range records {
			state := "active"
			if !r.RevokedAt.IsZero() {
				state = "revoked " + r.RevokedAt.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(out, "%s\tsha256:%s\t%s\n", r.Name, hex.EncodeToString(r.Fingerprint[:8]), state)
		}
	default:
		return fmt.Errorf("unknown connector command %q", sub)
	}
	return nil
}

// fingerprintFromPEM returns the fingerprint of the first CERTIFICATE block.
// Every block in the file is checked, not just the first: a private key
// anywhere in it is rejected so a mistaken argument cannot leave a key being
// read. Later certificates (a chain) must be valid but are not enrolled.
func fingerprintFromPEM(path string) (tunnel.Fingerprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tunnel.Fingerprint{}, errors.New("read connector certificate")
	}
	var fp tunnel.Fingerprint
	found := false
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return tunnel.Fingerprint{}, errors.New("file must contain only the public certificate")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return tunnel.Fingerprint{}, errors.New("invalid connector certificate")
		}
		if !found {
			fp, found = tunnel.FingerprintOf(block.Bytes), true
		}
	}
	if !found {
		return tunnel.Fingerprint{}, errors.New("no certificate found in file")
	}
	return fp, nil
}
