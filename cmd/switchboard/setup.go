package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

// runSetup creates an isolated, persistent, protected local installation. It
// never changes an existing installation, DNS, firewall or operating service.
func runSetup(ctx context.Context, args []string, out io.Writer) (err error) {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("dir", "my-edge", "new private installation directory")
	target := fs.String("target", "http://127.0.0.1:3000", "existing local application URL")
	site := fs.String("site", "", "static site name instead of a backend")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: switchboard setup [-dir DIR] [-target URL | -site NAME]")
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	// Validate all settings before creating even the directory.
	route := config.Route{Host: "localhost:8888", Target: *target, Access: &config.AccessPolicy{RequiredGroups: []string{"members"}}}
	if *site != "" {
		route.Target = ""
		route.Site = *site
	}
	socket := filepath.Join(root, "admin.sock")
	if runtime.GOOS == "windows" {
		socket = ""
	}
	cfg := config.Config{RuntimeFile: filepath.Join(root, "runtime.json"), Listen: "127.0.0.1:8888", Admin: config.Admin{Listen: config.DefaultAdminListen, Socket: socket, Token: "file:" + filepath.Join(root, "admin.token")},
		Auth:  config.Auth{Passkeys: &config.Passkeys{RPID: "localhost", LoginURL: "http://localhost:8888"}},
		Pages: &config.Pages{Dir: filepath.Join(root, "pages"), Keep: 5}, Routes: []config.Route{route}}
	validated, err := config.Validate(cfg)
	if err != nil {
		return err
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		return errors.New("setup needs a new directory; an existing installation is never overwritten")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(root)
		}
	}()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	if err := writeNewPrivateFile(filepath.Join(root, "admin.token"), []byte(base64.RawURLEncoding.EncodeToString(secret))); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(validated, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNewPrivateFile(filepath.Join(root, "config.json"), append(raw, '\n')); err != nil {
		return err
	}
	operational, err := config.OperatingJSON(*validated)
	if err != nil {
		return err
	}
	if err := writeNewPrivateFile(validated.RuntimeFile, operational); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(root, "pages"), 0o700); err != nil {
		return err
	}
	dbPath := filepath.Join(root, "state.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err == nil {
		_, err = sqlite.Migrate(ctx, db, migrations)
	}
	closeErr := db.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	guide := `# Your private Switchboard installation

This is a persistent local trial, bound to loopback. It does not install a
system service or move your existing website. Keep this directory private.

1. Start your existing application at the configured target (default port 3000).
2. Run Switchboard with the config and state paths printed by setup.
3. Run console login -config /path/to/config.json -name YOUR_NAME on this host.
   Open its one-use private link, then choose Applications. The session lasts
   15 minutes and stays in this browser tab; closing/reloading the tab locks it.
4. From the host, invite a visitor with group members (command printed by setup).
   Send the one-use invitation over a trusted channel; it expires in 15 minutes.
5. The visitor creates a passkey and opens http://localhost:8888/. Without a
   session the app asks for login; a person without members must be refused.
6. In People, edit membership, review the consequences and confirm that previous
   sessions end. A visitor account does not authorize console changes.
7. Stop and restart Switchboard with the same paths: state and deployments stay.

Static trial: setup -site NAME. Upload the built dist through Sites or the site
CLI with -token file:/absolute/path/to/admin.token. No build runs on the edge.

Before real hosting: choose the permanent login domain and RP ID, HTTPS and
cookie scope; credentials for localhost do not migrate to your public domain.
Follow docs/operations/systemd.md and docs/operations/first-application.md.
Do not expose port 9090. Use an SSH local forward to reach a remote console.

Recovery: use user invite -recover through the host socket, then revoke lost
credentials. Keep a second passkey. On Windows, the host Unix socket is not
available: offline recovery requires the stopped process and its state DB.

Backup: use switchboard db backup for SQLite; also retain config, referenced
secret files, pages and any certificate state using a private backup destination.
Test restore in a fresh directory. To undo a new route remove it through Routes
or restore the previous config and reload; DNS is a separate operator action.
`
	if err := writeNewPrivateFile(filepath.Join(root, "START-HERE.md"), []byte(guide)); err != nil {
		return err
	}
	complete = true
	fmt.Fprintln(out, "Created a protected local installation. No existing service or DNS was changed.")
	fmt.Fprintf(out, "Start: switchboard -config %q -db %q\n", filepath.Join(root, "config.json"), dbPath)
	if socket != "" {
		fmt.Fprintf(out, "Operator login (after start): switchboard console login -config %q -name YOUR_NAME\n", filepath.Join(root, "config.json"))
		fmt.Fprintf(out, "Invite (after start): switchboard user invite -config %q -email person@example.com -group members -return-to http://localhost:8888/\n", filepath.Join(root, "config.json"))
	}
	fmt.Fprintf(out, "Console: http://127.0.0.1:9090/applications\nGuide: %s\n", filepath.Join(root, "START-HERE.md"))
	return nil
}
