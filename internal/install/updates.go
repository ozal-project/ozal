package install

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/ozal-project/ozal/internal/config"
	relversion "github.com/ozal-project/ozal/internal/release"
	"github.com/ozal-project/ozal/internal/updateexec"
)

//go:embed ozal-update-executor.service
var executorUnit string

// releaseKey is the project's release public key, the same file published as
// docs/operations/ozal-release.pub. A binary that is already trusted on the
// host carries the key that its successors must be signed with.
//
//go:embed ozal-release.pub
var releaseKey string

// DefaultCatalogBase is where the project publishes its signed catalogs.
const DefaultCatalogBase = "https://ozal.dev/api/v1/releases"

// The fixed layout of a service installation (`ozal install service`) that
// updates turn on. Nothing here is chosen by a request.
const (
	executorUnitPath    = "/etc/systemd/system/ozal-update-executor.service"
	executorProfilePath = "/etc/ozal/update-executor.json"
	releaseKeyPath      = "/etc/ozal/release.pub"
	executorSocket      = "/run/ozal-update/executor.sock"
	installedConfig     = "/etc/ozal/config.json"
	installedBinary     = "/usr/local/lib/ozal/ozal"
	installedUnit       = "/etc/systemd/system/ozal.service"
)

// ExecutorUnit is the embedded systemd unit of the update executor.
func ExecutorUnit() string { return executorUnit }

// ReleaseKey is the embedded release public key.
func ReleaseKey() string { return strings.TrimSpace(releaseKey) }

// UpdatesRequest turns on installs from the console on an existing service
// installation: the root update executor, its profile and the pinned key,
// and admin.update_socket so the console can reach it.
type UpdatesRequest struct {
	// Root stages files under a directory without systemd changes.
	Root string
	// Channel is stable or preview; Installed (this binary's version) picks
	// it when empty: a prerelease follows preview.
	Channel, Installed string
	// PublicKey optionally replaces the embedded release key (a file with
	// the base64 key), for a project that signs its own releases.
	PublicKey string
	// CatalogBase is where the signed catalogs are read from.
	CatalogBase string
	Apply       bool
}

// UpdatesHost is what turning updates on needs from the machine.
type UpdatesHost interface {
	Systemctl(context.Context, ...string) error
	Healthy(context.Context) bool
}

type updatesPlan struct {
	request UpdatesRequest
	key     string
	profile []byte
	files   []plannedFile
}

type plannedFile struct {
	path string
	data []byte
	mode os.FileMode
}

func planUpdates(r UpdatesRequest) (updatesPlan, error) {
	if r.CatalogBase == "" {
		r.CatalogBase = DefaultCatalogBase
	}
	if r.Channel == "" {
		switch {
		case relversion.IsVersion(r.Installed) && strings.Contains(strings.SplitN(strings.TrimPrefix(r.Installed, "v"), "+", 2)[0], "-"):
			r.Channel = "preview"
		case relversion.IsVersion(r.Installed):
			r.Channel = "stable"
		default:
			return updatesPlan{}, errors.New("this build has no release version; choose the channel with -channel stable|preview")
		}
	}
	key := ReleaseKey()
	if r.PublicKey != "" {
		raw, err := os.ReadFile(r.PublicKey)
		if err != nil {
			return updatesPlan{}, fmt.Errorf("read the release public key: %w", err)
		}
		key = strings.TrimSpace(string(raw))
	}
	if _, err := relversion.ParsePublicKey(key); err != nil {
		return updatesPlan{}, err
	}
	profile := updateexec.Profile{
		Socket: executorSocket, SocketGroup: "ozal", Unit: "ozal", Binary: installedBinary,
		Config: installedConfig, DB: "/var/lib/ozal/state.db", User: "ozal",
		PublicKey: releaseKeyPath, CatalogBase: r.CatalogBase, Channel: r.Channel,
		Status: "/var/lib/ozal/update-status.json", State: "/var/lib/ozal/update-catalog-state.json",
		Jobs: "/var/lib/ozal/update-jobs", Lock: "/usr/local/lib/ozal/update.lock",
	}
	// Profile.Validate also inspects the files on this host, which do not
	// exist yet; the executor runs it when it starts. Here, only what this
	// request chose: the channel and the catalog address.
	if r.Channel != "stable" && r.Channel != "preview" {
		return updatesPlan{}, errors.New("-channel must be stable or preview")
	}
	if u, err := url.Parse(r.CatalogBase); err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return updatesPlan{}, errors.New("-catalog must be an absolute HTTPS URL without credentials, query or fragment")
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return updatesPlan{}, err
	}
	data = append(data, '\n')
	return updatesPlan{request: r, key: key, profile: data, files: []plannedFile{
		{releaseKeyPath, []byte(key + "\n"), 0o644},
		{executorProfilePath, data, 0o600},
		{executorUnitPath, []byte(executorUnit), 0o644},
	}}, nil
}

// Updates turns on installing signed releases from the console. It only adds
// to an existing installation: each file it writes must be new or already
// identical, and admin.update_socket must be unset or already point to the
// executor. A different key, profile or unit is refused, never replaced.
func Updates(ctx context.Context, out io.Writer, host UpdatesHost, r UpdatesRequest) error {
	if runtime.GOOS != "linux" && r.Root == "" {
		return errors.New("the update executor runs on Linux with systemd only")
	}
	plan, err := planUpdates(r)
	if err != nil {
		return err
	}
	r = plan.request
	for _, p := range []string{installedBinary, installedConfig, installedUnit} {
		actual := mapped(r.Root, p)
		if err := noLinks(actual); err != nil {
			return err
		}
		if info, err := os.Lstat(actual); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%s is missing: install the service first (ozal install service)", p)
		}
	}
	pending := []plannedFile{}
	for _, f := range plan.files {
		actual := mapped(r.Root, f.path)
		if err := noLinks(actual); err != nil {
			return err
		}
		existing, err := os.ReadFile(actual)
		switch {
		case errors.Is(err, os.ErrNotExist):
			pending = append(pending, f)
		case err != nil:
			return fmt.Errorf("cannot read %s: %w", f.path, err)
		case f.path == releaseKeyPath && strings.TrimSpace(string(existing)) == plan.key:
		case bytes.Equal(existing, f.data):
		case f.path == releaseKeyPath:
			return fmt.Errorf("%s pins a different release key; remove it only if you know why it was changed", f.path)
		default:
			return fmt.Errorf("%s already exists with other settings; review it by hand instead of letting the installer replace it", f.path)
		}
	}
	configPath := mapped(r.Root, installedConfig)
	edited, changed, err := withUpdateSocket(configPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Turn on updates from the console (channel %s, catalog %s):\n", r.Channel, r.CatalogBase)
	for _, f := range plan.files {
		state := "already in place"
		for _, p := range pending {
			if p.path == f.path {
				state = "create"
			}
		}
		fmt.Fprintf(out, "  %-48s %s\n", f.path, state)
	}
	if changed {
		fmt.Fprintf(out, "  %-48s set admin.update_socket to %s\n", installedConfig, executorSocket)
	} else {
		fmt.Fprintf(out, "  %-48s admin.update_socket already set\n", installedConfig)
	}
	if r.Root == "" {
		fmt.Fprintln(out, "  systemctl daemon-reload; enable --now ozal-update-executor; restart ozal (the edge is briefly unavailable)")
	}
	if !r.Apply {
		fmt.Fprintln(out, "Plan only. Run again with -apply to make these changes.")
		return nil
	}
	if r.Root == "" && (os.Geteuid() != 0 || host == nil) {
		return errors.New("turning on updates needs root on the host: run it with sudo")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, f := range pending {
		actual := mapped(r.Root, f.path)
		if err := os.MkdirAll(filepath.Dir(actual), 0o755); err != nil {
			return err
		}
		if err := newFile(actual, f.data, f.mode, nil); err != nil {
			return fmt.Errorf("%s: %w", f.path, err)
		}
	}
	if changed {
		if err := replaceKeepingOwner(configPath, edited); err != nil {
			return err
		}
	}
	if r.Root != "" {
		fmt.Fprintln(out, "Staged. No service was started.")
		return nil
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", "ozal-update-executor.service"}, {"restart", "ozal.service"}} {
		if err := host.Systemctl(ctx, args...); err != nil {
			return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
		}
	}
	if !host.Healthy(ctx) {
		return errors.New("ozal did not come back after the restart; check: journalctl -u ozal -n 50")
	}
	fmt.Fprintln(out, "Updates are on. In the console, open the update notice or Settings to review and install a signed release; installing needs the updates.install tool and a passkey.")
	return nil
}

// withUpdateSocket returns the configuration with admin.update_socket set to
// the executor's socket, keeping every other key in its place and value. It
// refuses a different socket and a result that does not validate.
func withUpdateSocket(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, false, fmt.Errorf("%s: %w", installedConfig, err)
	}
	switch cfg.Admin.UpdateSocket {
	case executorSocket:
		return raw, false, nil
	case "":
	default:
		return nil, false, fmt.Errorf("admin.update_socket already points to %s; leave it or change it by hand", cfg.Admin.UpdateSocket)
	}
	edited, err := setNested(raw, []string{"admin", "update_socket"}, executorSocket)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", installedConfig, err)
	}
	// The edit must change that one setting and nothing else.
	var check config.Config
	if err := json.Unmarshal(edited, &check); err != nil || check.Admin.UpdateSocket != executorSocket {
		return nil, false, errors.New("the configuration could not be edited safely; set admin.update_socket by hand")
	}
	check.Admin.UpdateSocket = ""
	if !reflect.DeepEqual(check, cfg) {
		return nil, false, errors.New("the configuration could not be edited safely; set admin.update_socket by hand")
	}
	return edited, true, nil
}

// setNested sets one string at a key path in a JSON object and keeps the
// order of every other key, so an operator's file reads the same afterwards.
func setNested(raw []byte, keys []string, value string) ([]byte, error) {
	members, err := objectMembers(raw)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(value)
	found := false
	for i, m := range members {
		if m.key != keys[0] {
			continue
		}
		found = true
		if len(keys) == 1 {
			members[i].value = encoded
			break
		}
		inner, err := setNested(m.value, keys[1:], value)
		if err != nil {
			return nil, err
		}
		members[i].value = inner
	}
	if !found {
		var v json.RawMessage = encoded
		for i := len(keys) - 1; i > 0; i-- {
			wrapped, _ := json.Marshal(map[string]json.RawMessage{keys[i]: v})
			v = wrapped
		}
		members = append(members, member{key: keys[0], value: v})
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(m.key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, b.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	return append(pretty.Bytes(), '\n'), nil
}

type member struct {
	key   string
	value json.RawMessage
}

func objectMembers(raw []byte) ([]member, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	open, err := decoder.Token()
	if err != nil || open != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var members []member
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("not a JSON object")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		members = append(members, member{key: key, value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return members, nil
}

// replaceKeepingOwner writes data beside path and renames it over path with
// the original's mode and owner, so a crash leaves either file whole.
func replaceKeepingOwner(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := chownLike(tmp, info); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
