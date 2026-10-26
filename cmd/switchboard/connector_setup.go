package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/tunnelrenew"
)

func runConnectorSetup(_ context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("connector "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("dir", "", "new private directory on the connector host")
	name := fs.String("name", "", "registry name for the connector")
	csr := fs.String("csr", "", "public CSR file on the signing host")
	ca := fs.String("ca-dir", "", "existing private CA directory, signing host only")
	certificate := fs.String("out", "", "new public certificate output")
	roots := fs.String("ca", "", "trusted CA certificate for local verification")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected connector setup arguments")
	}
	switch args[0] {
	case "prepare":
		if *dir == "" || config.ValidateConnectorName(*name) != nil {
			return errors.New("prepare needs -dir NEW_DIRECTORY -name CONNECTOR")
		}
		root, err := filepath.Abs(*dir)
		if err != nil {
			return err
		}
		if err := tunnelrenew.PrepareConnector(root); err != nil {
			return err
		}
		fmt.Fprintf(out, "Private key stays on this machine. Transfer only %s to the authorized signing host.\nAfter signing, return connector.crt and the public CA certificate here, then run connector check before enrollment.\n", filepath.Join(root, "connector.csr"))
	case "sign":
		if *csr == "" || *ca == "" || *certificate == "" {
			return errors.New("sign needs -csr PUBLIC_CSR -ca-dir LOCAL_CA_DIRECTORY -out NEW_CERTIFICATE")
		}
		if err := tunnelrenew.SignConnector(*ca, *csr, *certificate); err != nil {
			return err
		}
		fmt.Fprintln(out, "Public connector certificate created. Return it to its originating host, verify the key pair there, then enroll its fingerprint on the edge.")
	case "check":
		if *dir == "" || *roots == "" {
			return errors.New("check needs -dir IDENTITY_DIRECTORY -ca PUBLIC_CA_CERTIFICATE")
		}
		if err := tunnelrenew.CheckConnector(*dir, *roots); err != nil {
			return err
		}
		fmt.Fprintln(out, "Connector certificate matches its local key and configured CA, role, expiry and lifetime. Enroll its public certificate on the edge; this check alone does not authorize access.")
	}
	return nil
}
