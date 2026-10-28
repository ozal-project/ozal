package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

// ConfigPlan is what `sbctl edge config` would install. The operator keeps
// the host settings (listeners, TLS, identity, trusted proxies, DNS) in a
// local base file; routes, projects and the GitHub binding belong to the edge
// and live in its runtime file, edited from the console. The candidate root
// carries the live ones only as the seed of a runtime file that does not
// exist yet.
type ConfigPlan struct {
	Candidate      []byte // root config to install
	Validate       []byte // the same without runtime_file, for a check as the SSH user
	Seed           []byte // runtime seed from the live routes and projects
	Runtime        string // runtime_file path
	SeedRoutes     int    // routes carried into a new runtime file
	Restart        bool   // a reload cannot apply the change
	Unchanged      bool
	Changes        []string // top-level fields that differ
	ProxiesAdded   []string
	ProxiesRemoved []string
}

// ErrNoRuntimeFile refuses a base without runtime_file: in single-file mode
// every install would replace what the console saved.
var ErrNoRuntimeFile = errors.New("the base config must declare runtime_file: routes and projects belong to the edge, not to this file")

// PlanConfig builds the candidate root from base and the live root config.
// ranges, when not nil, replaces trusted_proxies (the proxy provider's
// published list).
func PlanConfig(live, base []byte, ranges []string) (ConfigPlan, error) {
	current, err := config.Parse(live)
	if err != nil {
		return ConfigPlan{}, fmt.Errorf("the live config does not parse: %w", err)
	}
	var candidate config.Config
	decoder := json.NewDecoder(bytes.NewReader(base))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidate); err != nil {
		return ConfigPlan{}, fmt.Errorf("base config: %w", err)
	}
	if candidate.RuntimeFile == "" {
		return ConfigPlan{}, ErrNoRuntimeFile
	}
	if len(candidate.Routes) > 0 || len(candidate.Projects) > 0 || candidate.GitHub != nil {
		return ConfigPlan{}, errors.New("the base config must not list routes, projects or github: the edge owns them (Applications in the console)")
	}
	if ranges != nil {
		candidate.TrustedProxies = slices.Clone(ranges)
	}
	candidate.Routes, candidate.Projects, candidate.GitHub = current.Routes, current.Projects, current.GitHub

	validated, err := config.Validate(candidate)
	if err != nil {
		return ConfigPlan{}, fmt.Errorf("candidate config: %w", err)
	}
	plan := ConfigPlan{Runtime: validated.RuntimeFile, SeedRoutes: len(validated.Routes), Restart: !config.SameServer(*current, *validated)}
	if plan.Candidate, err = marshal(candidate); err != nil {
		return ConfigPlan{}, err
	}
	check := candidate
	check.RuntimeFile = ""
	if plan.Validate, err = marshal(check); err != nil {
		return ConfigPlan{}, err
	}
	if plan.Seed, err = config.OperatingJSON(*validated); err != nil {
		return ConfigPlan{}, err
	}
	// Both sides go through the same struct, so a field written out as its
	// zero value on one side and omitted on the other is no change.
	var liveDoc config.Config
	_ = json.Unmarshal(live, &liveDoc)
	liveJSON, err := marshal(liveDoc)
	if err != nil {
		return ConfigPlan{}, err
	}
	plan.Changes = changedFields(liveJSON, plan.Candidate)
	plan.Unchanged = len(plan.Changes) == 0
	plan.ProxiesAdded, plan.ProxiesRemoved = difference(validated.TrustedProxies, current.TrustedProxies)
	return plan, nil
}

func marshal(cfg config.Config) ([]byte, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// changedFields compares the two documents field by field, so a different
// formatting of the same values is no change.
func changedFields(a, b []byte) []string {
	var left, right map[string]any
	_ = json.Unmarshal(a, &left)
	_ = json.Unmarshal(b, &right)
	var changed []string
	for key := range right {
		if !reflect.DeepEqual(left[key], right[key]) {
			changed = append(changed, key)
		}
	}
	for key := range left {
		if _, ok := right[key]; !ok {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}

func difference(next, previous []string) (added, removed []string) {
	in := func(list []string, v string) bool { return slices.Contains(list, v) }
	for _, v := range next {
		if !in(previous, v) {
			added = append(added, v)
		}
	}
	for _, v := range previous {
		if !in(next, v) {
			removed = append(removed, v)
		}
	}
	return added, removed
}

// ParseRanges reads a provider's published list, one CIDR per line, and
// refuses anything else: a truncated or replaced page must not become the
// list of addresses whose forwarded-for header the edge believes.
func ParseRanges(lists ...string) ([]string, error) {
	var out []string
	for _, list := range lists {
		for _, line := range strings.Split(list, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			prefix, err := netip.ParsePrefix(line)
			if err != nil || prefix.Bits() == 0 {
				return nil, fmt.Errorf("proxy range list has an invalid entry %q", line)
			}
			out = append(out, prefix.Masked().String())
		}
	}
	if len(out) < 4 {
		return nil, errors.New("proxy range list is unexpectedly short; refusing to replace trusted_proxies")
	}
	return out, nil
}

// Exit codes of ConfigRootScript.
const (
	ConfigInEffect      = 0
	ConfigRefused       = 2 // nothing changed
	ConfigRestored      = 3 // the new config was not taken; the previous one is in effect
	ConfigRestoreFailed = 4 // the previous config is back but the edge does not answer
)

// ConfigRootScript installs a root config as root under one sudo prompt.
// Root copies the staged files and checks their hashes first. When the
// runtime file does not exist yet it creates it from the seed, owned by the
// service account, so the routes in effect carry over. The installed binary
// validates the result with the real runtime file before anything is
// replaced. After the reload or restart it waits until the edge reports, on
// its admin socket, the digest of the files on disk; otherwise it restores the
// previous config, removes a runtime file it created and applies again.
// Arguments: staged config, its sha256, config path, staged seed, its sha256,
// runtime path, service account, unit, reload|restart, admin socket, backup
// stamp, installed binary.
const ConfigRootScript = `
set -u
trap '' HUP
STAGED=$1 SHA=$2 CONFIG=$3 SEED=$4 SEEDSHA=$5 RUNTIME=$6 OWNER=$7 UNIT=$8 MODE=$9 SOCKET=${10} STAMP=${11} BINARY=${12}
W=$(mktemp -d) || exit 2
created=0
done_() { rm -rf "$W"; }
unseed() { if [ "$created" = 1 ]; then rm -f "$RUNTIME"; fi; }
active() {
  curl -s --max-time 3 --unix-socket "$SOCKET" -H 'Host: localhost' http://localhost/_/api/status |
    python3 -c 'import json,sys; print(json.load(sys.stdin).get("config_sha256",""))' 2>/dev/null
}
wait_for() {
  i=0
  while [ "$i" -lt "${SBCTL_WAIT:-30}" ]; do
    [ "$(active)" = "$1" ] && return 0
    i=$((i + 1))
    sleep 1
  done
  return 1
}
digest() { { cat "$CONFIG"; printf '\000operating\000'; cat "$RUNTIME"; } | sha256sum | cut -d' ' -f1; }
apply() { if [ "$MODE" = reload ]; then systemctl reload "$UNIT"; else systemctl restart "$UNIT"; fi; }
OLD=$(active)
if [ -z "$OLD" ]; then echo "    the edge does not answer on $SOCKET: refused"; done_; exit 2; fi
cp "$STAGED" "$W/config.json" || { done_; exit 2; }
if [ "$(sha256sum "$W/config.json" | cut -d' ' -f1)" != "$SHA" ]; then echo "    the staged config changed after its upload: refused"; done_; exit 2; fi
if [ ! -e "$RUNTIME" ]; then
  cp "$SEED" "$W/runtime.json" || { done_; exit 2; }
  if [ "$(sha256sum "$W/runtime.json" | cut -d' ' -f1)" != "$SEEDSHA" ]; then echo "    the staged runtime seed changed after its upload: refused"; done_; exit 2; fi
  install -o "$OWNER" -g "$OWNER" -m 600 "$W/runtime.json" "$RUNTIME" || { done_; exit 2; }
  created=1
  echo "    created $RUNTIME from the routes and projects in effect"
fi
if ! "$BINARY" validate -config "$W/config.json" > /dev/null 2> "$W/err"; then
  echo "    the installed binary refused the config: $(head -c 400 "$W/err")"
  unseed; done_; exit 2
fi
cp -p "$CONFIG" "$CONFIG.bak-$STAMP" || { unseed; done_; exit 2; }
install -o root -g root -m 644 "$W/config.json" "$CONFIG.new" && mv -f "$CONFIG.new" "$CONFIG" || { unseed; done_; exit 2; }
WANT=$(digest)
echo "    installed; previous kept as $CONFIG.bak-$STAMP; $MODE $UNIT"
if apply && wait_for "$WANT"; then done_; exit 0; fi
echo "    the edge did not put the new config in effect: restoring the previous one"
cp -p "$CONFIG.bak-$STAMP" "$CONFIG"
unseed
apply
done_
wait_for "$OLD" && exit 3
exit 4
`

// ConfigCommand is the one command run with the operator's terminal.
func ConfigCommand(e Edge, staged, sha, seed, seedSHA, runtime, mode, stamp string) string {
	args := []string{staged, sha, e.Config, seed, seedSHA, runtime, e.ServiceUser, e.Unit, mode, e.Socket, stamp, e.Binary}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = Quote(a)
	}
	return "sudo sh -c " + Quote(ConfigRootScript) + " config " + strings.Join(quoted, " ")
}

// ConfigOutcome turns the root script's status into the operator's sentence.
func ConfigOutcome(code int, e Edge, stamp string) error {
	switch code {
	case ConfigInEffect:
		return nil
	case ConfigRefused:
		return errors.New("refused before installing (no sudo, a changed upload, a config the edge binary rejects, or an edge that does not answer): nothing changed")
	case ConfigRestored:
		return fmt.Errorf("the edge did not take the new config; the previous one is back in effect (%s.bak-%s)", e.Config, stamp)
	case ConfigRestoreFailed:
		return fmt.Errorf("the previous config was restored but the edge does not answer: journalctl -u %s", e.Unit)
	default:
		return fmt.Errorf("config install ended with status %d (255: SSH failed); check %s by hand", code, e.Config)
	}
}

// ConfigRequest is one run of `sbctl edge config`.
type ConfigRequest struct {
	Name   string
	Target Target
	Base   []byte
	Ranges []string // nil keeps the base's trusted_proxies
	Apply  bool
	Owner  string
	Stamp  string
}

// ApplyConfig plans and, with Apply, installs the root config on one edge.
func ApplyConfig(ctx context.Context, out io.Writer, remote Remote, req ConfigRequest) error {
	e := req.Target.Edge
	step := func(format string, a ...any) { fmt.Fprintf(out, "==> "+format+"\n", a...) }
	note := func(format string, a ...any) { fmt.Fprintf(out, "    "+format+"\n", a...) }
	if e.Socket == "" {
		return errors.New("edge.socket is required: the install confirms the config in effect on the admin socket")
	}

	step("read the live config")
	live, err := remote.Run(ctx, "cat "+Quote(e.Config), nil)
	if err != nil {
		return err
	}
	plan, err := PlanConfig([]byte(live), req.Base, req.Ranges)
	if err != nil {
		return err
	}
	for _, p := range plan.ProxiesAdded {
		note("trusted proxy added: %s (allow it in the firewall too)", p)
	}
	for _, p := range plan.ProxiesRemoved {
		note("trusted proxy removed: %s (remove it from the firewall too)", p)
	}
	if plan.Unchanged {
		step("the edge already runs this config: nothing to do")
		return nil
	}
	mode := "reload"
	if plan.Restart {
		mode = "restart"
	}
	note("changes: %s", strings.Join(plan.Changes, ", "))
	note("routes and projects live in %s; a missing one is created from the %d routes in effect", plan.Runtime, plan.SeedRoutes)
	if !req.Apply {
		step("plan: install the config and %s %s", mode, e.Unit)
		note("re-run with -apply to do it")
		return nil
	}

	got, err := remote.Run(ctx, LockAcquire(e.Lock, req.Owner), nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(got) != "taken" {
		return fmt.Errorf("%w (%s); if that process is gone, remove %s by hand after checking the edge", ErrBusy, strings.TrimSpace(got), e.Lock)
	}
	defer func() { _, _ = remote.Run(context.WithoutCancel(ctx), LockRelease(e.Lock, req.Owner), nil) }()

	staged, seed, check := e.Stage+"/config.next", e.Stage+"/runtime.seed", e.Stage+"/config.check"
	for _, f := range []struct {
		path string
		data []byte
	}{{staged, plan.Candidate}, {seed, plan.Seed}, {check, plan.Validate}} {
		if _, err := remote.Run(ctx, "umask 077; mkdir -p "+Quote(e.Stage)+" && cat > "+Quote(f.path), bytes.NewReader(f.data)); err != nil {
			return err
		}
	}
	step("validate with the edge's binary")
	if result, err := remote.Run(ctx, Quote(e.Binary)+" validate -config "+Quote(check), nil); err != nil {
		return fmt.Errorf("the edge's binary refused the config: %w", err)
	} else {
		note("%s", strings.TrimSpace(result))
	}
	step("install as root and %s %s (sudo asks for the password)", mode, e.Unit)
	code, err := remote.Terminal(ctx, ConfigCommand(e, staged, sha(plan.Candidate), seed, sha(plan.Seed), plan.Runtime, mode, req.Stamp))
	if err != nil {
		return fmt.Errorf("%w: staged in %s, nothing installed", ErrTerminalRequired, e.Stage)
	}
	if err := ConfigOutcome(code, e, req.Stamp); err != nil {
		return err
	}
	step("%s reports the new config", e.Unit)
	return nil
}
