package install

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/updateexec"
)

// The embedded unit and key are the published ones: an edge enabled by the
// installer runs the documented executor and pins the documented key.
func TestEmbeddedExecutorFilesMatchTheDocumentedOnes(t *testing.T) {
	for embedded, published := range map[string]string{
		ExecutorUnit():      "../../docs/operations/ozal-update-executor.service",
		ReleaseKey() + "\n": "../../docs/operations/ozal-release.pub",
	} {
		raw, err := os.ReadFile(published)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != embedded {
			t.Fatalf("%s differs from the copy built into the installer", published)
		}
	}
}

const installedConfigJSON = `{
  "listen": ":443",
  "runtime_file": "/var/lib/ozal/runtime.json",
  "admin": {
    "listen": "127.0.0.1:9090",
    "socket": "/run/ozal/admin.sock",
    "token": "file:/etc/ozal/admin.token"
  },
  "tls": {
    "enabled": true
  }
}
`

// realTempDir is t.TempDir without symlinks: macOS puts it under /var, a link
// the installer refuses to write through.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func stagedInstallation(t *testing.T, configJSON string) string {
	t.Helper()
	root := realTempDir(t)
	for path, data := range map[string]string{
		installedBinary: "binary", installedUnit: "[Unit]\n", installedConfig: configJSON,
	} {
		actual := mapped(root, path)
		if err := os.MkdirAll(filepath.Dir(actual), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(actual, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestInstallUpdatesStagesTheExecutorAndTheSocket(t *testing.T) {
	root := stagedInstallation(t, installedConfigJSON)
	var out bytes.Buffer
	if err := Updates(context.Background(), &out, nil, UpdatesRequest{Root: root, Installed: "v0.1.0-alpha.14", Apply: true}); err != nil {
		t.Fatal(err)
	}
	profileRaw, err := os.ReadFile(mapped(root, executorProfilePath))
	if err != nil {
		t.Fatal(err)
	}
	// The executor reads it with unknown fields refused.
	var profile updateexec.Profile
	decoder := json.NewDecoder(bytes.NewReader(profileRaw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profile); err != nil {
		t.Fatal(err)
	}
	if profile.Channel != "preview" || profile.CatalogBase != DefaultCatalogBase || profile.PublicKey != releaseKeyPath || profile.Socket != executorSocket {
		t.Fatalf("profile = %+v", profile)
	}
	for path, mode := range map[string]os.FileMode{executorProfilePath: 0o600, releaseKeyPath: 0o644, executorUnitPath: 0o644} {
		info, err := os.Stat(mapped(root, path))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: %v mode %v, want %v", path, err, info.Mode().Perm(), mode)
		}
	}
	edited, _ := os.ReadFile(mapped(root, installedConfig))
	text := string(edited)
	if !strings.Contains(text, `"update_socket": "/run/ozal-update/executor.sock"`) {
		t.Fatalf("config = %s", text)
	}
	// Keys keep their order: the operator's file reads the same afterwards.
	if strings.Index(text, `"listen": ":443"`) > strings.Index(text, `"runtime_file"`) || strings.Index(text, `"runtime_file"`) > strings.Index(text, `"admin"`) || strings.Index(text, `"admin"`) > strings.Index(text, `"tls"`) {
		t.Fatalf("key order changed:\n%s", text)
	}

	// Running it again changes nothing and succeeds.
	out.Reset()
	if err := Updates(context.Background(), &out, nil, UpdatesRequest{Root: root, Installed: "v0.1.0-alpha.14", Apply: true}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(out.String(), "admin.update_socket already set") || strings.Contains(out.String(), " create") {
		t.Fatalf("second run output:\n%s", out.String())
	}
}

func TestInstallUpdatesPlansWithoutChanging(t *testing.T) {
	root := stagedInstallation(t, installedConfigJSON)
	var out bytes.Buffer
	if err := Updates(context.Background(), &out, nil, UpdatesRequest{Root: root, Installed: "v1.2.0"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "channel stable") || !strings.Contains(out.String(), "Plan only") {
		t.Fatalf("plan:\n%s", out.String())
	}
	if _, err := os.Stat(mapped(root, executorProfilePath)); !os.IsNotExist(err) {
		t.Fatal("a plan wrote the profile")
	}
	if raw, _ := os.ReadFile(mapped(root, installedConfig)); string(raw) != installedConfigJSON {
		t.Fatal("a plan edited the configuration")
	}
}

// Nothing the operator already chose is replaced: a different pinned key,
// a different socket, or a profile with other settings stops the installer
// before it writes anything.
func TestInstallUpdatesRefusesToReplaceOtherSettings(t *testing.T) {
	cases := map[string]func(root string){
		"other key": func(root string) {
			os.WriteFile(mapped(root, releaseKeyPath), []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"), 0o644)
		},
		"other profile": func(root string) {
			os.WriteFile(mapped(root, executorProfilePath), []byte("{}\n"), 0o600)
		},
		"other socket": func(root string) {
			os.WriteFile(mapped(root, installedConfig), []byte(strings.Replace(installedConfigJSON, `"token"`, `"update_socket": "/run/other.sock", "token"`, 1)), 0o644)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root := stagedInstallation(t, installedConfigJSON)
			mutate(root)
			before, _ := os.ReadFile(mapped(root, installedConfig))
			if err := Updates(context.Background(), &bytes.Buffer{}, nil, UpdatesRequest{Root: root, Installed: "v0.1.0-alpha.14", Apply: true}); err == nil {
				t.Fatal("an existing setting was replaced")
			}
			if after, _ := os.ReadFile(mapped(root, installedConfig)); !bytes.Equal(before, after) {
				t.Fatal("the configuration changed on a refusal")
			}
			if _, err := os.Stat(mapped(root, executorUnitPath)); !os.IsNotExist(err) {
				t.Fatal("the unit was written on a refusal")
			}
		})
	}
}

func TestInstallUpdatesNeedsAnInstallationAndAChannel(t *testing.T) {
	if err := Updates(context.Background(), &bytes.Buffer{}, nil, UpdatesRequest{Root: realTempDir(t), Installed: "v0.1.0"}); err == nil || !strings.Contains(err.Error(), "install the service first") {
		t.Fatalf("without an installation: %v", err)
	}
	root := stagedInstallation(t, installedConfigJSON)
	if err := Updates(context.Background(), &bytes.Buffer{}, nil, UpdatesRequest{Root: root, Installed: "dev"}); err == nil || !strings.Contains(err.Error(), "-channel") {
		t.Fatalf("a dev build without a channel: %v", err)
	}
	if err := Updates(context.Background(), &bytes.Buffer{}, nil, UpdatesRequest{Root: root, Installed: "dev", Channel: "nightly"}); err == nil {
		t.Fatal("an unknown channel was accepted")
	}
}

type fakeUpdatesHost struct {
	calls     []string
	unhealthy int
}

func (h *fakeUpdatesHost) Systemctl(_ context.Context, args ...string) error {
	h.calls = append(h.calls, strings.Join(args, " "))
	return nil
}

func (h *fakeUpdatesHost) Healthy(context.Context) bool {
	if h.unhealthy > 0 {
		h.unhealthy--
		return false
	}
	return true
}

// The restarted edge takes a moment to listen: the installer waits for it
// instead of failing on the first check, and restarts it only when the
// configuration changed.
func TestActivateUpdatesWaitsForTheRestartedEdge(t *testing.T) {
	host := &fakeUpdatesHost{unhealthy: 1}
	if err := activateUpdates(context.Background(), host, true); err != nil {
		t.Fatalf("an edge that answers a moment later was reported as down: %v", err)
	}
	if strings.Join(host.calls, "; ") != "daemon-reload; enable --now ozal-update-executor.service; restart ozal.service" {
		t.Fatalf("calls = %v", host.calls)
	}
	host = &fakeUpdatesHost{}
	if err := activateUpdates(context.Background(), host, false); err != nil {
		t.Fatal(err)
	}
	for _, call := range host.calls {
		if strings.HasPrefix(call, "restart") {
			t.Fatal("the edge was restarted although its configuration did not change")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := activateUpdates(ctx, &fakeUpdatesHost{unhealthy: 1 << 30}, true); err == nil {
		t.Fatal("an edge that never answers was reported as up")
	}
}

// The plan names a restart only when one will happen.
func TestActivationPlanMatchesWhatActivationDoes(t *testing.T) {
	for _, changed := range []bool{true, false} {
		host := &fakeUpdatesHost{}
		if err := activateUpdates(context.Background(), host, changed); err != nil {
			t.Fatal(err)
		}
		restarts := strings.Contains(strings.Join(host.calls, ";"), "restart")
		if strings.Contains(activationPlan(changed), "restart ozal") != restarts {
			t.Fatalf("changed=%v: plan %q, calls %v", changed, activationPlan(changed), host.calls)
		}
	}
}
