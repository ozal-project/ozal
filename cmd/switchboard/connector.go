package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/tunnel"
)

// runConnector implements `switchboard connector enroll|rotate|current|revoke|list`: manage
// which tunnel connector certificates the edge accepts. The edge notices a
// revocation within its revalidation interval, so no restart is needed.
//
// It is a client of the admin API (CLI-001); enrol, rotate and revoke are
// accepted on the admin unix socket only. -db changes the registry
// directly: the break-glass path, and for now the path of the standalone
// `tunnel edge` process and its renewal agent, which have no admin API.
func runConnector(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard connector enroll|rotate|current|revoke|list [flags]")
	}
	if args[0] == "prepare" || args[0] == "sign" || args[0] == "check" {
		return runConnectorSetup(ctx, args, out)
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("connector "+sub, flag.ContinueOnError)
	fs.SetOutput(out)
	af := addAdminFlags(fs)
	dbPath := fs.String("db", "", "offline: change this SQLite state database directly, bypassing the running process")
	name := fs.String("name", "", "connector name (a-z, 0-9, '-')")
	certPath := fs.String("cert", "", "connector certificate PEM (the public certificate only)")
	expected := fs.String("expected", "", "exact current certificate SHA-256 for atomic rotation")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}

	// Validate locally first: a key in -cert must never leave the file,
	// whichever path the change takes.
	var (
		cert       []byte
		fp         tunnel.Fingerprint
		previous   tunnel.Fingerprint
		needsName  = sub != "list"
		needsCert  = sub == "enroll" || sub == "rotate"
		validFlags = true
	)
	switch sub {
	case "current", "revoke", "list", "enroll":
	case "rotate":
		raw, err := hex.DecodeString(*expected)
		if err != nil || len(raw) != len(previous) {
			validFlags = false
		}
		copy(previous[:], raw)
	default:
		return fmt.Errorf("unknown connector command %q", sub)
	}
	if !validFlags || (needsName && *name == "") || (needsCert && *certPath == "") {
		return connectorUsage(sub)
	}
	if needsCert {
		var err error
		if cert, fp, err = readConnectorCert(*certPath); err != nil {
			return err
		}
	}

	if *dbPath != "" {
		return runConnectorOffline(ctx, sub, *dbPath, *name, previous, fp, out)
	}
	c, err := af.client()
	if err != nil {
		return err
	}
	path := "/_/api/connectors/" + url.PathEscape(*name)
	var answer struct {
		SHA256 string `json:"sha256"`
	}
	switch sub {
	case "current":
		err := c.do(ctx, http.MethodGet, path, nil, &answer)
		if isStatus(err, http.StatusNotFound) {
			fmt.Fprintln(out, "none")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(out, answer.SHA256)
	case "rotate":
		request := map[string]string{"expected_sha256": *expected, "certificate_pem": string(cert)}
		if err := c.do(ctx, http.MethodPost, path+"/rotate", request, &answer); err != nil {
			return err
		}
		fmt.Fprintf(out, "rotated %s (sha256:%s)\n", *name, hex.EncodeToString(fp[:8]))
	case "enroll":
		request := map[string]string{"name": *name, "certificate_pem": string(cert)}
		if err := c.do(ctx, http.MethodPost, "/_/api/connectors", request, &answer); err != nil {
			return err
		}
		fmt.Fprintf(out, "enrolled %s (sha256:%s)\n", *name, hex.EncodeToString(fp[:8]))
	case "revoke":
		if err := c.do(ctx, http.MethodPost, path+"/revoke", nil, &struct{}{}); err != nil {
			return err
		}
		fmt.Fprintf(out, "revoked %s\n", *name)
	case "list":
		var listed struct {
			Connectors []struct {
				Name    string    `json:"name"`
				SHA256  string    `json:"sha256"`
				Revoked time.Time `json:"revoked"`
			} `json:"connectors"`
		}
		if err := c.do(ctx, http.MethodGet, "/_/api/connectors", nil, &listed); err != nil {
			return err
		}
		for _, r := range listed.Connectors {
			fmt.Fprintf(out, "%s\tsha256:%s\t%s\n", r.Name, shortID(r.SHA256), connectorState(r.Revoked))
		}
	}
	return nil
}

func runConnectorOffline(ctx context.Context, sub, dbPath, name string, previous, fp tunnel.Fingerprint, out io.Writer) error {
	db, err := openMigratedDB(ctx, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	registry := tunnel.NewRegistry(db)
	var sink events.Sink = events.Discard
	if sub == "enroll" || sub == "rotate" || sub == "revoke" {
		sink = offlineNotice()
	}

	switch sub {
	case "current":
		current, err := registry.Current(ctx, name)
		if errors.Is(err, tunnel.ErrNotFound) {
			fmt.Fprintln(out, "none")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(out, hex.EncodeToString(current[:]))
	case "rotate":
		if err := registry.Rotate(ctx, name, previous, fp); err != nil {
			return err
		}
		emitOffline(sink, events.Event{Type: events.ConnectorRotated, Host: name, Reason: "sha256:" + hex.EncodeToString(fp[:8])})
		fmt.Fprintf(out, "rotated %s (sha256:%s)\n", name, hex.EncodeToString(fp[:8]))
	case "enroll":
		if err := registry.Enroll(ctx, name, fp); err != nil {
			return err
		}
		emitOffline(sink, events.Event{Type: events.ConnectorEnrolled, Host: name, Reason: "sha256:" + hex.EncodeToString(fp[:8])})
		fmt.Fprintf(out, "enrolled %s (sha256:%s)\n", name, hex.EncodeToString(fp[:8]))
	case "revoke":
		if err := registry.Revoke(ctx, name); err != nil {
			return err
		}
		emitOffline(sink, events.Event{Type: events.ConnectorRevoked, Host: name})
		fmt.Fprintf(out, "revoked %s\n", name)
	case "list":
		records, err := registry.List(ctx)
		if err != nil {
			return err
		}
		for _, r := range records {
			fmt.Fprintf(out, "%s\tsha256:%s\t%s\n", r.Name, hex.EncodeToString(r.Fingerprint[:8]), connectorState(r.RevokedAt))
		}
	}
	return nil
}

func connectorUsage(sub string) error {
	switch sub {
	case "rotate":
		return errors.New("rotate needs -name, -cert and -expected (64 hex characters)")
	case "enroll":
		return errors.New("enroll needs -name and -cert")
	default:
		return fmt.Errorf("%s needs -name", sub)
	}
}

func connectorState(revoked time.Time) string {
	if revoked.IsZero() {
		return "active"
	}
	return "revoked " + revoked.UTC().Format(time.RFC3339)
}

// readConnectorCert reads a certificate file and fingerprints it with the
// same rules the edge applies (tunnel.FingerprintFromPEM).
func readConnectorCert(path string) ([]byte, tunnel.Fingerprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, tunnel.Fingerprint{}, errors.New("read connector certificate")
	}
	fp, err := tunnel.FingerprintFromPEM(data)
	return data, fp, err
}

// fingerprintFromPEM fingerprints a certificate file.
func fingerprintFromPEM(path string) (tunnel.Fingerprint, error) {
	_, fp, err := readConnectorCert(path)
	return fp, err
}
