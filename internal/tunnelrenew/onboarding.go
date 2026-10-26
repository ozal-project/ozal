package tunnelrenew

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// PrepareConnector creates the private key on its destination host. Only the
// CSR and later the signed certificate travel; an existing directory is refused.
func PrepareConnector(directory string) error {
	if err := os.Mkdir(directory, 0o700); err != nil {
		return errors.New("connector preparation needs a new private directory")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()
	if _, err := generate("connector", directory); err != nil {
		return errors.New("could not prepare connector identity")
	}
	complete = true
	return nil
}

// SignConnector signs one public CSR with the existing pilot signer. It cannot
// issue a server identity or broaden the fixed connector EKU/name contract.
func SignConnector(caDirectory, csrPath, certificatePath string) error {
	raw, err := os.ReadFile(csrPath)
	if err != nil {
		return errors.New("cannot read public connector CSR")
	}
	signed, err := issue(caDirectory, "connector", string(raw), time.Now())
	if err != nil {
		return errors.New("connector signing failed; check CSR and local CA lifetime")
	}
	if err := writeNew(certificatePath, []byte(signed)); err != nil {
		return errors.New("certificate output must be a new writable file")
	}
	return nil
}

func CheckConnector(directory, caPath string) error {
	return validate("connector", filepath.Clean(directory), caPath, time.Now())
}
