package install

import (
	"context"
	"crypto/rand"
	"debug/elf"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

//go:embed ozal.service
var unitTemplate string

func Unit() string { return unitTemplate }

type ServiceRequest struct {
	Binary, Archive, SHA256, Root, Target, Group string
	Apply                                        bool
}
type ServicePlan struct {
	Request ServiceRequest
	Config  config.Config
}
type Account struct{ UID, GID int }
type ServiceHost interface {
	Account(context.Context) (Account, error)
	RunAs(context.Context, Account, string, ...string) error
	Systemctl(context.Context, ...string) error
	Healthy(context.Context) bool
}

func PlanService(r ServiceRequest) (ServicePlan, error) {
	if r.Target == "" {
		r.Target = "http://127.0.0.1:3000"
	}
	if r.Group == "" {
		r.Group = "members"
	}
	u, err := url.Parse(r.Target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ServicePlan{}, errors.New("target must be an http(s) application URL without credentials, query or fragment")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString(r.Group) {
		return ServicePlan{}, errors.New("group must be lowercase letters, digits, underscore or dash (1-32 characters)")
	}
	if (r.Binary == "") == (r.Archive == "") {
		return ServicePlan{}, errors.New("provide exactly one verified binary or archive")
	}
	if r.Archive != "" && !validChecksum(r.SHA256) {
		return ServicePlan{}, errors.New("archive installation requires a trusted SHA-256")
	}
	if r.Root != "" && (!filepath.IsAbs(r.Root) || filepath.Clean(r.Root) == string(filepath.Separator)) {
		return ServicePlan{}, errors.New("staging root must be absolute and must not be the host root")
	}
	cfg := config.Config{RuntimeFile: "/var/lib/ozal/runtime.json", Listen: "127.0.0.1:8888",
		Auth:  config.Auth{Passkeys: &config.Passkeys{RPID: "localhost", LoginURL: "http://localhost:8888", DisplayName: "Ozal"}},
		Admin: config.Admin{Listen: "127.0.0.1:9090", Socket: "/run/ozal/admin.sock", Token: "file:/etc/ozal/admin.token"},
		Pages: &config.Pages{Dir: "/var/lib/ozal/pages", Keep: 5}, Routes: []config.Route{{Host: "localhost:8888", Target: r.Target, Access: &config.AccessPolicy{RequiredGroups: []string{r.Group}}}}}
	normalized, err := config.Validate(cfg)
	if err != nil {
		return ServicePlan{}, errors.New("installation configuration is invalid")
	}
	return ServicePlan{Request: r, Config: *normalized}, nil
}

func mapped(root, path string) string {
	if root == "" {
		return path
	}
	return filepath.Join(root, path[1:])
}

func newFile(path string, data []byte, mode os.FileMode, owner *Account) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("installation output must be new and writable")
	}
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if owner != nil {
		if err := f.Chown(owner.UID, owner.GID); err != nil {
			return err
		}
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// noLinks refuses pre-existing symlink components before privileged writes.
func noLinks(path string) error {
	for p := filepath.Clean(path); p != filepath.Dir(p); p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return errors.New("cannot inspect installation path")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("installation paths must not traverse symlinks")
		}
	}
	return nil
}

func Service(ctx context.Context, out io.Writer, host ServiceHost, r ServiceRequest) error {
	plan, err := PlanService(r)
	if err != nil {
		return err
	}
	r = plan.Request
	if runtime.GOOS != "linux" {
		return errors.New("the systemd installer supports Linux only")
	}
	paths := []string{"/usr/local/lib/ozal", "/etc/ozal", "/var/lib/ozal", "/etc/systemd/system/ozal.service", "/usr/local/bin/ozal"}
	for _, p := range paths {
		actual := mapped(r.Root, p)
		if err := noLinks(actual); err != nil {
			return err
		}
		if _, err := os.Lstat(actual); !os.IsNotExist(err) {
			return errors.New("installation output exists; preserve its state and use ozal upgrade")
		}
	}
	for _, p := range []string{"/etc/switchboard/config.json", "/etc/systemd/system/switchboard.service", "/usr/local/lib/switchboard/switchboard"} {
		if _, err := os.Lstat(mapped(r.Root, p)); !os.IsNotExist(err) {
			return errors.New("legacy Switchboard installation found; upgrade it in place instead of creating parallel state")
		}
	}
	fmt.Fprintf(out, "Install root-owned code/config, private service state and loopback listeners (apply=%t, staging=%t).\n", r.Apply, r.Root != "")
	if !r.Apply {
		return nil
	}
	if r.Root == "" && (os.Geteuid() != 0 || host == nil) {
		return errors.New("a host administrator and Linux service adapter are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "ozal-service-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	binary := r.Binary
	if r.Archive != "" {
		binary = filepath.Join(work, "ozal")
		if err := Binary(io.Discard, BinaryRequest{Archive: r.Archive, SHA256: r.SHA256, Destination: binary, Apply: true}); err != nil {
			return err
		}
	}
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxBinaryBytes {
		return errors.New("verified binary must be a bounded regular file, not a symlink")
	}
	source, err := os.Open(binary)
	if err != nil {
		return errors.New("cannot read verified binary")
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("binary changed during review")
	}
	format, err := elf.NewFile(source)
	if err != nil {
		return errors.New("service binary must be a Linux ELF executable")
	}
	if r.Root == "" && ((runtime.GOARCH == "amd64" && format.Machine != elf.EM_X86_64) || (runtime.GOARCH == "arm64" && format.Machine != elf.EM_AARCH64)) {
		return errors.New("binary architecture does not match this host")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var account Account
	if r.Root == "" {
		account, err = host.Account(ctx)
		if err != nil {
			return err
		}
		if account.UID <= 0 || account.GID <= 0 {
			return errors.New("service account must be unprivileged")
		}
	}
	for _, p := range []string{"/usr/local/lib/ozal", "/etc/ozal", "/var/lib/ozal"} {
		actual := mapped(r.Root, p)
		if err := os.MkdirAll(filepath.Dir(actual), 0755); err != nil {
			return err
		}
		if err := os.Mkdir(actual, 0700); err != nil {
			return errors.New("installation directory was not created exclusively")
		}
	}
	for _, p := range []string{"/usr/local/lib/ozal", "/etc/ozal"} {
		if err := os.Chmod(mapped(r.Root, p), 0755); err != nil {
			return err
		}
	}
	for _, p := range []string{"/usr/local/bin", "/etc/systemd/system"} {
		if err := os.MkdirAll(mapped(r.Root, p), 0755); err != nil {
			return err
		}
	}
	if err := os.Mkdir(mapped(r.Root, "/var/lib/ozal/pages"), 0700); err != nil {
		return err
	}
	dest, err := os.OpenFile(mapped(r.Root, "/usr/local/lib/ozal/ozal"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	copied, copyErr := io.Copy(dest, io.LimitReader(source, MaxBinaryBytes+1))
	syncErr := dest.Sync()
	modeErr := dest.Chmod(0755)
	closeErr := dest.Close()
	if copied != info.Size() || copied > MaxBinaryBytes || copyErr != nil || syncErr != nil || modeErr != nil || closeErr != nil {
		return errors.New("binary installation failed; do not start the service")
	}
	if err := os.Symlink("/usr/local/lib/ozal/ozal", mapped(r.Root, "/usr/local/bin/ozal")); err != nil {
		return err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return errors.New("cannot generate administrative credential")
	}
	var tokenOwner, stateOwner *Account
	if r.Root == "" {
		tokenOwner = &Account{UID: 0, GID: account.GID}
		stateOwner = &account
	}
	if err := newFile(mapped(r.Root, "/etc/ozal/admin.token"), []byte(base64.RawURLEncoding.EncodeToString(random)), 0640, tokenOwner); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan.Config, "", "  ")
	if err != nil {
		return err
	}
	if err := newFile(mapped(r.Root, "/etc/ozal/config.json"), append(data, '\n'), 0644, nil); err != nil {
		return err
	}
	operating, err := config.OperatingJSON(plan.Config)
	if err != nil {
		return err
	}
	if err := newFile(mapped(r.Root, "/var/lib/ozal/runtime.json"), operating, 0600, stateOwner); err != nil {
		return err
	}
	if err := newFile(mapped(r.Root, "/etc/systemd/system/ozal.service"), []byte(Unit()), 0644, nil); err != nil {
		return err
	}
	if r.Root != "" {
		fmt.Fprintf(out, "Staged under %s; no account or service was changed.\n", r.Root)
		return nil
	}
	for _, p := range []string{"/var/lib/ozal", "/var/lib/ozal/pages"} {
		if err := os.Chown(p, account.UID, account.GID); err != nil {
			return err
		}
		if err := os.Chmod(p, 0700); err != nil {
			return err
		}
	}
	if err := host.RunAs(ctx, account, "/usr/local/lib/ozal/ozal", "migrate", "up", "-db", "/var/lib/ozal/state.db"); err != nil {
		return err
	}
	if err := host.Systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	stop := func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = host.Systemctl(cleanup, "stop", "ozal.service")
	}
	if err := host.Systemctl(ctx, "enable", "--now", "ozal.service"); err != nil {
		stop()
		return errors.New("service activation failed; state retained")
	}
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for !host.Healthy(deadline) {
		select {
		case <-deadline.Done():
			stop()
			return errors.New("service readiness failed; service stopped and state retained")
		case <-time.After(time.Second):
		}
	}
	fmt.Fprintln(out, "Ozal is running on loopback. Forward port 9090 over SSH; create the first administrator with sudo ozal console enrol. Back up /etc/ozal and /var/lib/ozal.")
	return nil
}
