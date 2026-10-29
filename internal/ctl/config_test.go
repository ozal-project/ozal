package ctl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

// The live edge as Server Ops rendered it: one file with routes inside.
const liveConfig = `{
  "listen": ":8888",
  "admin": {"listen": "127.0.0.1:9091", "token": "file:/etc/sb/admin.token"},
  "trusted_proxies": ["173.245.48.0/20"],
  "projects": [{"name": "portfolio", "site": "portfolio", "hostnames": ["portfolio.example.com"], "deployers": [{"token": "env:DEPLOY_TOKEN"}]}],
  "pages": {"dir": "/var/lib/sb/pages"},
  "routes": [
    {"host": "app.example.com", "target": "http://127.0.0.1:3000"},
    {"host": "portfolio.example.com", "site": "portfolio"}
  ]
}`

// The operator's base: host settings only.
const baseConfig = `{
  "runtime_file": "/var/lib/sb/runtime.json",
  "listen": ":8888",
  "admin": {"listen": "127.0.0.1:9091", "token": "file:/etc/sb/admin.token"},
  "trusted_proxies": ["173.245.48.0/20"],
  "pages": {"dir": "/var/lib/sb/pages"}
}`

func TestPlanConfigKeepsTheEdgesRoutes(t *testing.T) {
	plan, err := PlanConfig([]byte(liveConfig), []byte(baseConfig), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Restart || plan.Unchanged || plan.Runtime != "/var/lib/sb/runtime.json" || plan.SeedRoutes != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	// Adopting a runtime file seeds it with every route and project in
	// effect: nothing the edge serves is lost.
	var seed struct {
		Routes   []config.Route   `json:"routes"`
		Projects []config.Project `json:"projects"`
	}
	if err := json.Unmarshal(plan.Seed, &seed); err != nil || len(seed.Routes) != 2 || len(seed.Projects) != 1 {
		t.Fatalf("seed = %s (%v)", plan.Seed, err)
	}
	if _, err := config.Parse(plan.Candidate); err != nil {
		t.Fatalf("candidate does not parse: %v", err)
	}
	if strings.Contains(string(plan.Validate), "runtime_file") {
		t.Fatal("the SSH-user check copy still names the runtime file it cannot read")
	}

	// Installed, the same base is no change; a new proxy list reloads.
	again, err := PlanConfig(plan.Candidate, []byte(baseConfig), nil)
	if err != nil || !again.Unchanged {
		t.Fatalf("second plan = %+v, %v", again, err)
	}
	ranges, err := PlanConfig(plan.Candidate, []byte(baseConfig), []string{"173.245.48.0/20", "103.21.244.0/22", "2400:cb00::/32", "104.16.0.0/13"})
	if err != nil || ranges.Restart || ranges.Unchanged || len(ranges.ProxiesAdded) != 3 || len(ranges.ProxiesRemoved) != 0 {
		t.Fatalf("proxy refresh = %+v, %v", ranges, err)
	}
}

func TestPlanConfigRefusesABaseThatWouldOwnRoutes(t *testing.T) {
	single := strings.Replace(baseConfig, `"runtime_file": "/var/lib/sb/runtime.json",`, "", 1)
	if _, err := PlanConfig([]byte(liveConfig), []byte(single), nil); !errors.Is(err, ErrNoRuntimeFile) {
		t.Fatalf("single-file base: %v", err)
	}
	withRoutes := strings.Replace(baseConfig, `"listen": ":8888",`, `"listen": ":8888", "routes": [{"host": "x.example.com", "target": "http://127.0.0.1:1"}],`, 1)
	if _, err := PlanConfig([]byte(liveConfig), []byte(withRoutes), nil); err == nil {
		t.Fatal("a base listing routes was accepted: it would overwrite the console's")
	}
	if _, err := PlanConfig([]byte(liveConfig), []byte(`{"runtime_file": "/x", "typo": 1}`), nil); err == nil {
		t.Fatal("an unknown base field was accepted")
	}
}

func TestParseRangesRefusesAnythingButCIDRs(t *testing.T) {
	good := "173.245.48.0/20\n103.21.244.0/22\n"
	if got, err := ParseRanges(good, "2400:cb00::/32\n2606:4700::/32\n"); err != nil || len(got) != 4 {
		t.Fatalf("ranges = %v, %v", got, err)
	}
	for _, bad := range []string{good + "<html>", good + "0.0.0.0/0\n1.1.1.0/24\n", "173.245.48.0/20\n"} {
		if _, err := ParseRanges(bad, "2400:cb00::/32\n"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

type configRemote struct {
	live     string
	ran      []string
	terminal int
}

func (f *configRemote) Run(_ context.Context, command string, stdin io.Reader) (string, error) {
	f.ran = append(f.ran, command)
	if stdin != nil {
		_, _ = io.Copy(io.Discard, stdin)
	}
	switch {
	case strings.HasPrefix(command, "cat '/etc/ozal/routing.json'"):
		return f.live, nil
	case strings.Contains(command, "mkdir '/home/deploy/lock'"):
		return "taken\n", nil
	case strings.Contains(command, "validate -config"):
		return "config valid\n", nil
	}
	return "", nil
}

func (f *configRemote) Terminal(_ context.Context, command string) (int, error) {
	f.ran = append(f.ran, "TERMINAL "+command)
	return f.terminal, nil
}

func configTarget() Target {
	t := goodTarget()
	t.Edge.Socket = "/run/ozal/admin.sock"
	return t
}

func TestApplyConfigPlansReadOnlyAndAppliesInOrder(t *testing.T) {
	r := &configRemote{live: liveConfig}
	req := ConfigRequest{Target: configTarget(), Base: []byte(baseConfig), Owner: "me", Stamp: "s"}
	if err := ApplyConfig(context.Background(), io.Discard, r, req); err != nil || len(r.ran) != 1 {
		t.Fatalf("plan: %v, ran %v", err, r.ran)
	}
	r.ran = nil
	req.Apply = true
	if err := ApplyConfig(context.Background(), io.Discard, r, req); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range r.ran {
		for _, key := range []string{"TERMINAL", "validate -config", "cat '/etc", "mkdir '/home/deploy/lock'", "rmdir", "config.next", "runtime.seed", "config.check"} {
			if strings.Contains(c, key) {
				order = append(order, key)
				break
			}
		}
	}
	if got := strings.Join(order, ","); got != "cat '/etc,mkdir '/home/deploy/lock',config.next,runtime.seed,config.check,validate -config,TERMINAL,rmdir" {
		t.Fatalf("order: %s", got)
	}
	r = &configRemote{live: liveConfig, terminal: ConfigRestored}
	if err := ApplyConfig(context.Background(), io.Discard, r, req); err == nil || !strings.Contains(err.Error(), "previous one is back") {
		t.Fatalf("restored edge reported as: %v", err)
	}
	noSocket := req
	noSocket.Target = goodTarget()
	if err := ApplyConfig(context.Background(), io.Discard, &configRemote{live: liveConfig}, noSocket); err == nil {
		t.Fatal("a target without edge.socket cannot confirm the config in effect")
	}
}

// rootHost runs ConfigRootScript in a directory standing in for the edge:
// systemctl "applies" the config by publishing the digest the edge would
// report, curl reads it back, install ignores the owner (no root here).
type rootHost struct {
	t                         *testing.T
	dir, bin, config, runtime string
}

func newRootHost(t *testing.T) *rootHost {
	t.Helper()
	dir := t.TempDir()
	h := &rootHost{t: t, dir: dir, bin: filepath.Join(dir, "bin"), config: filepath.Join(dir, "routing.json"), runtime: filepath.Join(dir, "runtime.json")}
	if err := os.Mkdir(h.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"curl": `[ -e "$STATE/down" ] && exit 0; printf '{"config_sha256": "%s"}' "$(cat "$STATE/active")"`,
		"systemctl": `echo "$1" >> "$STATE/applied"
if [ -e "$STATE/broken" ]; then echo garbage > "$STATE/active"; exit 0; fi
if grep -q runtime_file "$FAKE_CONFIG"; then { cat "$FAKE_CONFIG"; printf '\000operating\000'; cat "$FAKE_RUNTIME"; } | sha256sum | cut -d' ' -f1 > "$STATE/active"
else sha256sum "$FAKE_CONFIG" | cut -d' ' -f1 > "$STATE/active"; fi`,
		"install": `mode=644
while [ $# -gt 2 ]; do case $1 in -o|-g) shift 2;; -m) mode=$2; shift 2;; *) break;; esac; done
cp "$1" "$2" && chmod "$mode" "$2"`,
		"ozal": `[ -e "$STATE/invalid" ] && { echo "config: nope" >&2; exit 1; }; echo ok`,
	} {
		if err := os.WriteFile(filepath.Join(h.bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(h.config, []byte(liveConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	h.setActive(digestOf([]byte(liveConfig)))
	return h
}

func digestOf(parts ...[]byte) string {
	sum := sha256.Sum256(bytes.Join(parts, nil))
	return hex.EncodeToString(sum[:])
}

func (h *rootHost) setActive(digest string) {
	if err := os.WriteFile(filepath.Join(h.dir, "active"), []byte(digest), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *rootHost) run(candidate, seed []byte, staleSHA bool) int {
	h.t.Helper()
	staged, seedPath := filepath.Join(h.dir, "config.next"), filepath.Join(h.dir, "runtime.seed")
	_ = os.WriteFile(staged, candidate, 0o600)
	_ = os.WriteFile(seedPath, seed, 0o600)
	want := sha(candidate)
	if staleSHA {
		want = sha([]byte("other"))
	}
	cmd := exec.Command("sh", "-c", ConfigRootScript, "config", staged, want, h.config, seedPath, sha(seed), h.runtime,
		"ozal", "ozal", "restart", filepath.Join(h.dir, "admin.sock"), "s1", filepath.Join(h.bin, "ozal"))
	cmd.Env = append(cmd.Environ(), "PATH="+h.bin+":"+os.Getenv("PATH"), "STATE="+h.dir, "FAKE_CONFIG="+h.config, "FAKE_RUNTIME="+h.runtime, "OZALCTL_WAIT=2")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		h.t.Logf("%s", out)
		return exit.ExitCode()
	}
	if err != nil {
		h.t.Fatalf("%v: %s", err, out)
	}
	return 0
}

func (h *rootHost) file(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func rootPlan(t *testing.T) ConfigPlan {
	t.Helper()
	plan, err := PlanConfig([]byte(liveConfig), []byte(strings.Replace(baseConfig, "/var/lib/sb/runtime.json", "/RUNTIME", 1)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestConfigRootScriptAdoptsTheRuntimeFileAndConfirmsTheDigest(t *testing.T) {
	h := newRootHost(t)
	plan := rootPlan(t)
	candidate := bytes.ReplaceAll(plan.Candidate, []byte("/RUNTIME"), []byte(h.runtime))
	if code := h.run(candidate, plan.Seed, false); code != ConfigInEffect {
		t.Fatalf("exit %d", code)
	}
	if h.file(h.config) != string(candidate) || h.file(h.runtime) != string(plan.Seed) || h.file(h.config+".bak-s1") != liveConfig {
		t.Fatal("config not installed, runtime not seeded or backup missing")
	}
	if info, _ := os.Stat(h.runtime); info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime file mode %v", info.Mode().Perm())
	}
	// A runtime file that exists is the console's state: never overwritten.
	if err := os.WriteFile(h.runtime, []byte(`{"routes":[],"projects":[],"github":null}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := h.run(candidate, plan.Seed, false); code != ConfigInEffect || strings.Contains(h.file(h.runtime), "app.example.com") {
		t.Fatalf("exit %d; existing runtime file replaced: %s", code, h.file(h.runtime))
	}
}

func TestConfigRootScriptRestoresWhenTheEdgeDoesNotTakeIt(t *testing.T) {
	h := newRootHost(t)
	plan := rootPlan(t)
	candidate := bytes.ReplaceAll(plan.Candidate, []byte("/RUNTIME"), []byte(h.runtime))
	_ = os.WriteFile(filepath.Join(h.dir, "broken"), nil, 0o644)
	// The edge never reports the new digest; once restored it reports the old.
	code := h.run(candidate, plan.Seed, false)
	if code != ConfigRestored && code != ConfigRestoreFailed {
		t.Fatalf("exit %d", code)
	}
	if h.file(h.config) != liveConfig {
		t.Fatal("the previous config was not restored")
	}
	if _, err := os.Stat(h.runtime); !os.IsNotExist(err) {
		t.Fatal("a runtime file created by the failed install was left behind")
	}
}

func TestConfigRootScriptRefusesBeforeChangingAnything(t *testing.T) {
	plan := rootPlan(t)
	for name, prepare := range map[string]func(h *rootHost) bool{
		"changed upload":     func(*rootHost) bool { return true },
		"binary refuses":     func(h *rootHost) bool { _ = os.WriteFile(filepath.Join(h.dir, "invalid"), nil, 0o644); return false },
		"edge not answering": func(h *rootHost) bool { _ = os.WriteFile(filepath.Join(h.dir, "down"), nil, 0o644); return false },
	} {
		h := newRootHost(t)
		stale := prepare(h)
		candidate := bytes.ReplaceAll(plan.Candidate, []byte("/RUNTIME"), []byte(h.runtime))
		if code := h.run(candidate, plan.Seed, stale); code != ConfigRefused {
			t.Errorf("%s: exit %d, want %d", name, code, ConfigRefused)
		}
		if h.file(h.config) != liveConfig || h.file(filepath.Join(h.dir, "applied")) != "" {
			t.Errorf("%s: the edge was touched", name)
		}
		if _, err := os.Stat(h.runtime); !os.IsNotExist(err) {
			t.Errorf("%s: a runtime file was left behind", name)
		}
	}
}
