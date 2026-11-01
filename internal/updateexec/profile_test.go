package updateexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validProfileFile(t *testing.T) (Profile, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "release.pub")
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "ozal"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A short directory for the socket: sun_path is 104 bytes on macOS and
	// t.TempDir() embeds the test name.
	socketDir, err := os.MkdirTemp("", "sb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	p := Profile{
		Socket: filepath.Join(socketDir, "executor.sock"), Unit: "ozal",
		Binary: filepath.Join(binDir, "ozal"), Config: filepath.Join(dir, "config.json"),
		PublicKey: key, CatalogBase: "https://ozal.dev/api/v1/releases",
		Channel: "stable", Status: filepath.Join(dir, "update-status.json"),
		State: filepath.Join(dir, "catalog-state.json"), Jobs: filepath.Join(dir, "jobs"),
	}
	path := filepath.Join(dir, "executor.json")
	raw := `{"socket":` + quote(p.Socket) + `,"unit":"ozal","binary":` + quote(p.Binary) + `,"config":` + quote(p.Config) +
		`,"public_key":` + quote(key) + `,"catalog_base":"https://ozal.dev/api/v1/releases","channel":"stable","status":` +
		quote(p.Status) + `,"state":` + quote(p.State) + `,"jobs":` + quote(p.Jobs) + `}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, path
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(value, `\`, `\\`) + `"`
}

func TestLoadProfileAcceptsARootOnlyFixedProfile(t *testing.T) {
	want, path := validProfileFile(t)
	got, err := LoadProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Socket != want.Socket || got.Channel != "stable" || got.Keep != 0 {
		t.Fatalf("profile %+v", got)
	}
	if url := got.CatalogURL("linux", "arm64"); url != "https://ozal.dev/api/v1/releases/stable/linux/arm64" {
		t.Fatalf("catalog URL %q", url)
	}
}

func TestLoadProfileRefusesWritableFiles(t *testing.T) {
	_, path := validProfileFile(t)
	if err := os.Chmod(path, 0o664); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfile(path); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadProfileRefusesUnknownFields(t *testing.T) {
	_, path := validProfileFile(t)
	raw, _ := os.ReadFile(path)
	raw = append(raw[:len(raw)-1], []byte(`,"command":"reboot"}`)...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfile(path); err == nil {
		t.Fatal("unknown profile field accepted")
	}
}

func TestProfileValidationRejectsInconsistentHosts(t *testing.T) {
	base, _ := validProfileFile(t)
	for _, test := range []struct {
		name   string
		mutate func(*Profile)
		want   string
	}{
		{"relative binary", func(p *Profile) { p.Binary = "ozal" }, "absolute"},
		{"db without user", func(p *Profile) { p.DB = filepath.Join(t.TempDir(), "state.db") }, "service account"},
		{"user without db", func(p *Profile) { p.User = "ozal" }, "no db"},
		{"plain HTTP catalog", func(p *Profile) { p.CatalogBase = "http://ozal.dev/api" }, "HTTPS"},
		{"credentials in catalog", func(p *Profile) { p.CatalogBase = "https://u:p@ozal.dev/api" }, "credentials"},
		{"query in catalog", func(p *Profile) { p.CatalogBase = "https://ozal.dev/api?x=1" }, "query"},
		{"unknown channel", func(p *Profile) { p.Channel = "nightly" }, "stable or preview"},
		{"too many kept binaries", func(p *Profile) { p.Keep = 21 }, "keep"},
		{"invalid group", func(p *Profile) { p.SocketGroup = "Bad Group" }, "socket_group"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := base
			test.mutate(&p)
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}

func TestProfileValidationNeedsAProtectedPublicKey(t *testing.T) {
	p, _ := validProfileFile(t)
	if err := os.Chmod(p.PublicKey, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "public_key") {
		t.Fatalf("err = %v", err)
	}
	if err := os.Remove(p.PublicKey); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err == nil {
		t.Fatal("missing public key accepted")
	}
}

func TestProfileValidationProtectsTheRootBinary(t *testing.T) {
	p, _ := validProfileFile(t)
	if err := os.Chmod(p.Binary, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "binary is writable") {
		t.Fatalf("writable binary: %v", err)
	}
	if err := os.Chmod(p.Binary, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("non-executable binary: %v", err)
	}
	if err := os.Chmod(p.Binary, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(p.Binary)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "binary directory is writable") {
		t.Fatalf("writable binary directory: %v", err)
	}
}
