package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/cloudflare"
	"github.com/ozal-project/ozal/internal/config"
)

// runCloudflare implements `ozal cloudflare enable`: the host's half of
// connecting Cloudflare. The console checks and stores the token; this
// command, run as root, moves the edge's certificates to DNS-01 through
// Cloudflare. It changes how the public listener gets certificates and
// needs a restart, so it stays with the host, like creating the first
// administrator. Without -apply it only shows what would change.
func runCloudflare(ctx context.Context, args []string, out io.Writer) error {
	return runCloudflareWithAPI(ctx, args, out, "")
}

func runCloudflareWithAPI(ctx context.Context, args []string, out io.Writer, apiBase string) error {
	if len(args) == 0 || args[0] != "enable" {
		return errors.New("usage: ozal cloudflare enable -token-file FILE [-target ADDRESS] [-config FILE] [-apply] [-restart UNIT]")
	}
	fs := flag.NewFlagSet("cloudflare enable", flag.ContinueOnError)
	fs.SetOutput(out)
	configPath := fs.String("config", installedConfig, "host configuration to change")
	tokenFile := fs.String("token-file", "", "the token the console stored (absolute path)")
	target := fs.String("target", "", "this edge's public address (dns.target), when the configuration has none")
	apply := fs.Bool("apply", false, "write the change; without it the plan is only shown")
	restart := fs.String("restart", "", "systemd unit to restart after writing (for example ozal.service)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*tokenFile) {
		return errors.New("-token-file must be the absolute path the console printed")
	}
	if *restart != "" && !unitPattern.MatchString(*restart) {
		return fmt.Errorf("-restart %q is not a systemd unit name", *restart)
	}
	token, err := readTokenFile(*tokenFile)
	if err != nil {
		return err
	}
	client, err := certs.NewCloudflareClient(certs.CloudflareConfig{Token: token, BaseURL: apiBase})
	if err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.VerifyToken(checkCtx); err != nil {
		return fmt.Errorf("cloudflare did not accept the token: %w", err)
	}
	zones, err := client.ZoneNames(checkCtx)
	if err != nil {
		return err
	}
	hostData, err := os.ReadFile(*configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	host, err := config.Parse(hostData)
	if err != nil {
		return err
	}
	merged, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	stateDir := filepath.Join(filepath.Dir(*tokenFile), "dns01")
	plan, err := cloudflare.PlanEnable(*host, *merged, "file:"+*tokenFile, stateDir, *target, zones)
	if errors.Is(err, cloudflare.ErrAlreadyActive) {
		fmt.Fprintln(out, "Certificates by DNS-01 through Cloudflare are already active with this token. Nothing to change.")
		return nil
	}
	if err != nil {
		return err
	}
	printEnablePlan(out, plan, stateDir)
	if !*apply {
		fmt.Fprintln(out, "\nNothing was changed. Run again with -apply to write it.")
		return nil
	}
	backup, err := backupConfig(*configPath)
	if err != nil {
		return err
	}
	if err := makeStateDir(stateDir, *tokenFile); err != nil {
		return err
	}
	if err := config.WriteAtomic(*configPath, plan.Host); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nWrote %s (the previous version is %s).\n", *configPath, backup)
	if *restart == "" {
		fmt.Fprintln(out, "Restart Ozal to obtain the new certificates.")
		return nil
	}
	cmd := exec.CommandContext(ctx, "systemctl", "restart", "--", *restart)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restart %s: %w; to go back, restore %s and restart", *restart, err, backup)
	}
	fmt.Fprintf(out, "Restarted %s. The console's Domains page now follows the certificates.\n", *restart)
	return nil
}

var unitPattern = regexp.MustCompile(`^[A-Za-z0-9@._:-]{1,128}$`)

func printEnablePlan(out io.Writer, plan cloudflare.Enabled, stateDir string) {
	fmt.Fprintln(out, "Certificates by DNS-01 through Cloudflare:")
	for _, zone := range plan.Zones {
		var hosts []string
		for host, z := range plan.Covers {
			if z == zone {
				hosts = append(hosts, host)
			}
		}
		slices.Sort(hosts)
		fmt.Fprintf(out, "  %s and *.%s  covers %s\n", zone, zone, strings.Join(hosts, ", "))
	}
	if len(plan.Local) > 0 {
		fmt.Fprintf(out, "  No public certificate needed: %s\n", strings.Join(plan.Local, ", "))
	}
	fmt.Fprintf(out, "Changes: tls.domains, tls.dns01 (state in %s), dns.cloudflare and dns.target %s.\n", stateDir, plan.Host.DNS.Target)
}

// backupConfig keeps the configuration as it was, beside it, with the same
// permissions and owner, so the change can be undone by renaming it back.
func backupConfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	backup := path + ".before-dns01-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.WriteFile(backup, data, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("back up config: %w", err)
	}
	if err := sameOwner(backup, info); err != nil {
		return "", err
	}
	return backup, nil
}

// The token file and the state directory live where the service user
// writes, and this command runs as root: it must not follow a link the
// service user planted there, or root would read another file (and send it
// to Cloudflare as the token) or hand another directory to the service.

// tokenPattern matches a Cloudflare API token, as the console checks it.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,200}$`)

// readTokenFile reads the token only from a regular file reached without
// any symbolic link, and only if it looks like a token.
func readTokenFile(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved != path {
		return "", fmt.Errorf("%s must be the token file itself, reached without symbolic links", path)
	}
	root, err := tokenDirectory(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return readScopedToken(root, filepath.Base(path))
}

// tokenDirectory pins the checked parent directory. Renaming it or replacing
// it with a link afterwards cannot redirect root's reads or ownership changes.
func tokenDirectory(path string) (*os.Root, error) {
	dir := filepath.Dir(path)
	if resolved, err := filepath.EvalSymlinks(dir); err != nil || resolved != dir {
		return nil, fmt.Errorf("%s must be reached without symbolic links", dir)
	}
	before, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, fmt.Errorf("the token directory changed; run again")
	}
	return root, nil
}

func readScopedToken(root *os.Root, name string) (string, error) {
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() > 512 {
		return "", errors.New("the token must be a regular file, reached without symbolic links")
	}
	file, err := root.Open(name)
	if err != nil {
		return "", fmt.Errorf("open the token: %w", err)
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return "", errors.New("the token file changed; run again")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 513))
	if err != nil || len(raw) > 512 {
		return "", errors.New("the token file could not be read")
	}
	token := strings.TrimSpace(string(raw))
	if !tokenPattern.MatchString(token) {
		return "", errors.New("the file does not hold a Cloudflare API token")
	}
	return token, nil
}

// makeStateDir creates the certificate state directory for the service
// user, the owner of the token file. An existing one must be a real
// directory; the owner is set with Lchown, which never follows a link, so a
// link swapped in at the last moment changes only itself.
func makeStateDir(dir, tokenFile string) error {
	if filepath.Dir(dir) != filepath.Dir(tokenFile) {
		return errors.New("certificate state must be beside the token")
	}
	root, err := tokenDirectory(tokenFile)
	if err != nil {
		return err
	}
	defer root.Close()
	return makeScopedStateDir(root, filepath.Base(dir), filepath.Base(tokenFile))
}

func makeScopedStateDir(root *os.Root, name, token string) error {
	info, err := root.Lstat(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := root.Mkdir(name, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("%s exists and is not a directory; remove it and run again", name)
	}
	owner, err := root.Lstat(token)
	if err != nil || !owner.Mode().IsRegular() {
		return errors.New("the token file is no longer a regular file")
	}
	return sameOwnerInRoot(root, name, owner)
}

// verifyCloudflareToken is the console's check: an active token, and the
// zones it can read.
func verifyCloudflareToken(ctx context.Context, token string) ([]string, error) {
	client, err := certs.NewCloudflareClient(certs.CloudflareConfig{Token: token})
	if err != nil {
		return nil, err
	}
	if err := client.VerifyToken(ctx); err != nil {
		return nil, err
	}
	return client.ZoneNames(ctx)
}

// hostCloudflareCommand builds the command the console shows, with this
// binary, this configuration and, when known, the unit to restart.
func hostCloudflareCommand(configPath, unit string) func(tokenFile, target string) string {
	prefix := hostActionCommand(configPath, "cloudflare enable")
	if prefix == "" {
		return nil
	}
	return func(tokenFile, target string) string {
		command := prefix + " -token-file " + shellArg(tokenFile)
		if target != "" {
			command += " -target " + shellArg(target)
		}
		command += " -apply"
		if unit != "" {
			command += " -restart " + shellArg(unit)
		}
		return command
	}
}

// systemdUnit is the unit this process runs as, read from its cgroup, or ""
// outside systemd.
func systemdUnit() string {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	return unitFromCgroup(string(data))
}

// unitFromCgroup finds the .service in a /proc/self/cgroup listing.
func unitFromCgroup(listing string) string {
	for _, line := range strings.Split(listing, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		for _, part := range strings.Split(parts[2], "/") {
			if strings.HasSuffix(part, ".service") && unitPattern.MatchString(part) {
				return part
			}
		}
	}
	return ""
}

// cloudflareStateDir also makes a relative -db path usable by the absolute
// token path required by the host command.
func cloudflareStateDir(dbPath string) (string, error) {
	dir, err := filepath.Abs(filepath.Dir(dbPath))
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}
