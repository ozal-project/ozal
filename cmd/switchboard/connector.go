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

	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
	"github.com/octaviosantacruz/switchboard/internal/tunnel"
)

// runConnector implements `switchboard connector enroll|revoke|list`: manage
// which tunnel connector certificates the edge accepts. The edge notices a
// revocation within its revalidation interval, so no restart is needed.
func runConnector(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard connector enroll|revoke|list [flags]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("connector "+sub, flag.ContinueOnError)
	fs.SetOutput(out)
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	name := fs.String("name", "", "connector name (a-z, 0-9, '-')")
	certPath := fs.String("cert", "", "connector certificate PEM (the public certificate only)")
	if err := fs.Parse(rest); err != nil {
		return err
	}

	db, err := sqlite.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	// Refuse to operate on a database whose schema is not current: running
	// migrations implicitly from an admin command would hide a deploy step.
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		return err
	}
	statuses, err := sqlite.MigrationStatus(ctx, db, migrations)
	if err != nil {
		return err
	}
	for _, s := range statuses {
		if !s.Applied {
			return errors.New("database has pending migrations: run `switchboard migrate` first")
		}
	}
	registry := tunnel.NewRegistry(db)

	switch sub {
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

// fingerprintFromPEM reads the first CERTIFICATE block. A private key in the
// file is rejected so a mistaken argument cannot leave a key being read.
func fingerprintFromPEM(path string) (tunnel.Fingerprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tunnel.Fingerprint{}, errors.New("read connector certificate")
	}
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return tunnel.Fingerprint{}, errors.New("no certificate found in file")
		}
		if block.Type != "CERTIFICATE" {
			return tunnel.Fingerprint{}, errors.New("file must contain only the public certificate")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return tunnel.Fingerprint{}, errors.New("invalid connector certificate")
		}
		return tunnel.FingerprintOf(block.Bytes), nil
	}
}
