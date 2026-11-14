package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func TestUnitFromCgroup(t *testing.T) {
	for listing, want := range map[string]string{
		"0::/system.slice/ozal-example.service\n":                           "ozal-example.service",
		"12:pids:/system.slice/ozal.service\n0::/system.slice/ozal.service": "ozal.service",
		"0::/user.slice/user-1000.slice/session-3.scope\n":                  "",
		"":                                  "",
		"0::/system.slice/bad;name.service": "",
	} {
		if got := unitFromCgroup(listing); got != want {
			t.Errorf("%q = %q, want %q", listing, got, want)
		}
	}
}

func TestHostCloudflareCommandNamesEverythingTheHostNeeds(t *testing.T) {
	build := hostCloudflareCommand("/etc/ozal-example/config.json", "ozal-example.service")
	if build == nil {
		t.Skip("no executable path in this environment")
	}
	got := build("/var/lib/ozal-example/cloudflare.token", "203.0.113.10")
	for _, want := range []string{"sudo ", " cloudflare enable", "-config /etc/ozal-example/config.json", "-token-file /var/lib/ozal-example/cloudflare.token", "-target 203.0.113.10", "-apply", "-restart ozal-example.service"} {
		if !strings.Contains(got, want) {
			t.Errorf("command %q lacks %q", got, want)
		}
	}
}

// Without -apply nothing changes; with it the host file gains DNS-01 through
// Cloudflare, keeps its permissions, and the previous version stays beside it.
func TestCloudflareEnableShowsThenApplies(t *testing.T) {
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/user/tokens/verify" {
			w.Write([]byte(`{"success":true,"result":{"status":"active"}}`))
			return
		}
		w.Write([]byte(`{"success":true,"result":[{"name":"example.org"}]}`))
	}))
	defer cf.Close()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(dir, "cloudflare.token")
	if err = os.WriteFile(tokenFile, []byte("abcdefghijklmnopqrstuvwxyz0123456789ABCD\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	original := []byte(`{"listen": ":443", "admin": {"token": "secret"},
		"tls": {"enabled": true, "cache_dir": "` + dir + `/acme", "email": "ops@example.com", "domains": ["auth.lab.example.org"]},
		"auth": {"passkeys": {"rp_id": "lab.example.org", "login_url": "https://auth.lab.example.org"}},
		"routes": [{"host": "app.lab.example.org", "target": "http://127.0.0.1:3000"}]}`)
	if err := os.WriteFile(configPath, original, 0o640); err != nil {
		t.Fatal(err)
	}
	run := func(extra ...string) (string, error) {
		var out bytes.Buffer
		args := append([]string{"enable", "-config", configPath, "-token-file", tokenFile, "-target", "203.0.113.10"}, extra...)
		err := runCloudflareWithAPI(context.Background(), args, &out, cf.URL)
		return out.String(), err
	}

	out, err := run()
	if err != nil || !strings.Contains(out, "lab.example.org and *.lab.example.org") || !strings.Contains(out, "Nothing was changed") {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if data, _ := os.ReadFile(configPath); !bytes.Equal(data, original) {
		t.Fatal("the plan changed the configuration")
	}

	if out, err = run("-apply"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS.DNS01 == nil || cfg.TLS.DNS01.DNSToken != "file:"+tokenFile || cfg.DNS == nil || cfg.DNS.Target != "203.0.113.10" {
		t.Fatalf("written config: %+v %+v", cfg.TLS.DNS01, cfg.DNS)
	}
	if info, _ := os.Stat(configPath); info.Mode().Perm() != 0o640 {
		t.Fatalf("permissions changed to %v", info.Mode().Perm())
	}
	backups, _ := filepath.Glob(configPath + ".before-dns01-*")
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	if data, _ := os.ReadFile(backups[0]); !bytes.Equal(data, original) {
		t.Fatal("the backup is not the previous configuration")
	}
	if info, err := os.Stat(filepath.Join(dir, "dns01")); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("state dir: %v %v", info, err)
	}

	if out, err = run("-apply"); err != nil || !strings.Contains(out, "already active") {
		t.Fatalf("second run: %v\n%s", err, out)
	}
}

func TestCloudflareEnableRefusesABadUnitName(t *testing.T) {
	var out bytes.Buffer
	err := runCloudflare(context.Background(), []string{"enable", "-token-file", "/x", "-restart", "ozal; rm -rf /"}, &out)
	if err == nil || !strings.Contains(err.Error(), "not a systemd unit") {
		t.Fatalf("err = %v", err)
	}
}

// The service user writes where the token and the state directory live, and
// this command runs as root: a link planted there is refused, so root reads
// no other file as the token and hands no other directory to the service.
func TestCloudflareEnableRefusesPlantedLinks(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "root-only")
	if err := os.WriteFile(secret, []byte("abcdefghijklmnopqrstuvwxyz0123456789ABCD\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "cloudflare.token")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("no symbolic links here")
	}
	if _, err := readTokenFile(link); err == nil || !strings.Contains(err.Error(), "without symbolic links") {
		t.Fatalf("a linked token file was read: %v", err)
	}
	if _, err := readTokenFile(secret); err != nil {
		t.Fatalf("a plain token file was refused: %v", err)
	}
	notToken := filepath.Join(dir, "passwd")
	os.WriteFile(notToken, []byte("root:x:0:0:root:/root:/bin/bash\n"), 0o600)
	if _, err := readTokenFile(notToken); err == nil {
		t.Fatal("a file that is not a token was read as one")
	}

	elsewhere := t.TempDir()
	state := filepath.Join(dir, "dns01")
	if err := os.Symlink(elsewhere, state); err != nil {
		t.Fatal(err)
	}
	if err := makeStateDir(state, secret); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("a linked state directory was accepted: %v", err)
	}
	os.Remove(state)
	if err := makeStateDir(state, secret); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(state); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("state dir: %v %v", info, err)
	}
}

func TestCloudflareStateStaysInTheCheckedDirectoryAcrossRename(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir, moved, other := filepath.Join(base, "state"), filepath.Join(base, "original"), filepath.Join(base, "other")
	for _, path := range []string{dir, other} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const original = "abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	const unrelated = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcd"
	for path, value := range map[string]string{filepath.Join(dir, "cloudflare.token"): original, filepath.Join(other, "cloudflare.token"): unrelated} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := tokenDirectory(filepath.Join(dir, "cloudflare.token"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, dir); err != nil {
		t.Skip("symbolic links unavailable")
	}
	if got, err := readScopedToken(root, "cloudflare.token"); err != nil || got != original {
		t.Fatalf("token read escaped the checked directory: %v", err)
	}
	if err := makeScopedStateDir(root, "dns01", "cloudflare.token"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "dns01")); err != nil {
		t.Fatal("state was not created in the checked directory")
	}
	if _, err := os.Stat(filepath.Join(other, "dns01")); !os.IsNotExist(err) {
		t.Fatal("state creation followed the replacement link")
	}
}

func TestCloudflareHostCommandKeepsPathsAsLiteralArguments(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	configPath := filepath.Join(t.TempDir(), "config with 'quotes' $(printf injected).json")
	tokenFile := filepath.Join(t.TempDir(), "token with spaces; printf injected")
	build := hostCloudflareCommand(configPath, "ozal.service")
	if build == nil {
		t.Fatal("no host command")
	}
	// Capture arguments without executing sudo or the edge binary.
	output, err := exec.Command("sh", "-c", `sudo() { printf '%s\0' "$@"; }; `+build(tokenFile, "203.0.113.10")).Output()
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	for flag, want := range map[string]string{"-config": configPath, "-token-file": tokenFile, "-target": "203.0.113.10", "-restart": "ozal.service"} {
		found := false
		for i := range args {
			if args[i] == flag && i+1 < len(args) && args[i+1] == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s was not one literal argument: %q", flag, args)
		}
	}
}

func TestCloudflareRelativeDatabaseProducesAnAbsoluteTokenDirectory(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	got, err := cloudflareStateDir("state.db")
	if err != nil || got != dir || !filepath.IsAbs(got) {
		t.Fatalf("relative database directory: %q %v", got, err)
	}
	linked := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(dir, linked); err != nil {
		t.Skip("symbolic links unavailable")
	}
	got, err = cloudflareStateDir(filepath.Join(linked, "state.db"))
	if err != nil || got != dir {
		t.Fatalf("linked database directory: %q %v", got, err)
	}
}
