package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func release(t *testing.T, kind byte, duplicate bool) (string, string) {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	count := 1
	if duplicate {
		count = 2
	}
	for i := 0; i < count; i++ {
		h := &tar.Header{Name: "ozal", Typeflag: kind, Mode: 0755, Linkname: "/bin/sh"}
		if kind == tar.TypeReg {
			h.Size = 6
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			tw.Write([]byte("binary"))
		}
	}
	tw.Close()
	gz.Close()
	path := filepath.Join(t.TempDir(), "release.tgz")
	os.WriteFile(path, b.Bytes(), 0600)
	sum := sha256.Sum256(b.Bytes())
	return path, hex.EncodeToString(sum[:])
}
func TestBinaryChecksumNoOverwriteAndArchiveSafety(t *testing.T) {
	for _, tc := range []struct {
		name           string
		kind           byte
		duplicate, bad bool
	}{{"valid", tar.TypeReg, false, false}, {"bad checksum", tar.TypeReg, false, true}, {"link", tar.TypeSymlink, false, false}, {"duplicate", tar.TypeReg, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			archive, sum := release(t, tc.kind, tc.duplicate)
			if tc.bad {
				sum = strings.Repeat("0", 64)
			}
			dest := filepath.Join(t.TempDir(), "ozal")
			err := Binary(io.Discard, BinaryRequest{Archive: archive, SHA256: sum, Destination: dest, Apply: true})
			valid := tc.kind == tar.TypeReg && !tc.duplicate && !tc.bad
			if (err == nil) != valid {
				t.Fatal(err)
			}
			if valid {
				if err := Binary(io.Discard, BinaryRequest{Archive: archive, SHA256: sum, Destination: dest, Apply: true}); err == nil {
					t.Fatal("overwrote binary")
				}
			} else {
				if _, err := os.Lstat(dest); !os.IsNotExist(err) {
					t.Fatal("published invalid binary")
				}
			}
		})
	}
}
func TestServiceStagingPlanPermissionsAndLegacyRefusal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux installer")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "stage")
	req := ServiceRequest{Binary: exe, Root: root, Target: "http://127.0.0.1:4000", Group: "staff"}
	var plan bytes.Buffer
	if err := Service(context.Background(), &plan, nil, req); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("plan wrote files")
	}
	for _, want := range []string{"/usr/local/lib/ozal/ozal", "/usr/local/bin/ozal", "/etc/ozal/config.json", "/etc/ozal/admin.token", "/var/lib/ozal/runtime.json", "/etc/systemd/system/ozal.service", "127.0.0.1:9090", `http://localhost:8888 forwards to http://127.0.0.1:4000 and admits only people in the group "staff"`} {
		if !strings.Contains(plan.String(), want) {
			t.Fatalf("plan does not name %q:\n%s", want, plan.String())
		}
	}
	req.Apply = true
	var out bytes.Buffer
	if err := Service(context.Background(), &out, nil, req); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{"etc/ozal/admin.token": 0640, "var/lib/ozal": 0700, "var/lib/ozal/runtime.json": 0600, "usr/local/lib/ozal/ozal": 0755} {
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", path, info, err)
		}
	}
	token, _ := os.ReadFile(filepath.Join(root, "etc/ozal/admin.token"))
	cfg, _ := os.ReadFile(filepath.Join(root, "etc/ozal/config.json"))
	if bytes.Contains(cfg, bytes.TrimSpace(token)) || strings.Contains(out.String(), string(bytes.TrimSpace(token))) {
		t.Fatal("secret exposed")
	}
	if err := Service(context.Background(), io.Discard, nil, req); err == nil {
		t.Fatal("overwrote service")
	}
	legacy := t.TempDir()
	os.MkdirAll(filepath.Join(legacy, "etc/switchboard"), 0700)
	os.WriteFile(filepath.Join(legacy, "etc/switchboard/config.json"), nil, 0600)
	req.Root = legacy
	if err := Service(context.Background(), io.Discard, nil, req); err == nil {
		t.Fatal("shadowed old state")
	}
	if _, err := os.Stat(filepath.Join(legacy, "etc/ozal")); !os.IsNotExist(err) {
		t.Fatal("changed legacy installation")
	}
	docs, err := os.ReadFile("../../docs/operations/ozal.service")
	if err == nil && string(docs) != Unit() {
		t.Fatal("documented and installed unit differ")
	}
}

// The last thing a new operator reads says where the trial route goes, who
// it admits and what an empty upstream looks like.
func TestServiceReadyNamesTheTrialRoute(t *testing.T) {
	plan, err := PlanService(ServiceRequest{Binary: "/tmp/ozal", Target: "http://127.0.0.1:4000", Group: "staff"})
	if err != nil {
		t.Fatal(err)
	}
	got := serviceReady(plan)
	for _, want := range []string{`http://localhost:8888 forwards to http://127.0.0.1:4000 and admits only people in the group "staff"`, "Until an application listens on 127.0.0.1:4000", "forward port 9090", "sudo ozal console enrol", "/etc/ozal and /var/lib/ozal"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ready message does not contain %q:\n%s", want, got)
		}
	}
}

func TestBinaryRejectsCorruptGzipFooterBeforePublishing(t *testing.T) {
	archive, _ := release(t, tar.TypeReg, false)
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-8] ^= 0xff
	if err := os.WriteFile(archive, data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	dest := filepath.Join(t.TempDir(), "ozal")
	if err := Binary(io.Discard, BinaryRequest{Archive: archive, SHA256: hex.EncodeToString(sum[:]), Destination: dest, Apply: true}); err == nil {
		t.Fatal("published corrupt gzip")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("invalid binary became visible")
	}
}

func TestUninstallPlanAndStagedRemovalPreserveStateUnlessPurged(t *testing.T) {
	tempRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tempRoot, "stage")
	for _, path := range []string{"etc/systemd/system/ozal.service", "etc/ozal/config.json", "var/lib/ozal/state.db", "usr/local/lib/ozal/ozal", "usr/local/lib/ozal/ozal.bak-0123456789ab", "usr/local/lib/ozal/ozal.upgrade.log", "usr/local/lib/ozal/update.lock"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("sample"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	binDir := filepath.Join(root, "usr/local/bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/local/lib/ozal/ozal", filepath.Join(binDir, "ozal")); err != nil {
		t.Fatal(err)
	}
	var plan bytes.Buffer
	if err := Uninstall(context.Background(), &plan, nil, UninstallRequest{Root: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "usr/local/lib/ozal/ozal")); err != nil {
		t.Fatal("plan changed installation", err)
	}
	for _, want := range []string{"remove ozal.service", "/usr/local/bin/ozal (command link)", "ozal.bak-0123456789ab", "update.lock", "keep /etc/ozal and /var/lib/ozal"} {
		if !strings.Contains(plan.String(), want) {
			t.Fatalf("uninstall plan does not name %q:\n%s", want, plan.String())
		}
	}
	if strings.Contains(plan.String(), "ozal-update.timer") {
		t.Fatalf("uninstall plan names a unit that is not installed:\n%s", plan.String())
	}
	plan.Reset()
	if err := Uninstall(context.Background(), &plan, nil, UninstallRequest{Root: root, PurgeState: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "remove permanently /etc/ozal and /var/lib/ozal") {
		t.Fatalf("purge plan does not say state is removed:\n%s", plan.String())
	}
	if err := Uninstall(context.Background(), io.Discard, nil, UninstallRequest{Root: root, Apply: true}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"etc/systemd/system/ozal.service", "usr/local/bin/ozal", "usr/local/lib/ozal"} {
		if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("installer artifact %s remains: %v", path, err)
		}
	}
	for _, path := range []string{"etc/ozal/config.json", "var/lib/ozal/state.db"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("default uninstall removed preserved state %s: %v", path, err)
		}
	}
	if err := Uninstall(context.Background(), io.Discard, nil, UninstallRequest{Root: root, Apply: true, PurgeState: true}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"etc/ozal", "var/lib/ozal"} {
		if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("purged state %s remains: %v", path, err)
		}
	}
}

func TestUninstallRefusesUnrelatedCommandFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "usr/local/bin/ozal")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unrelated"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(context.Background(), io.Discard, nil, UninstallRequest{Root: root, Apply: true}); err == nil {
		t.Fatal("removed a non-installer command file")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("unrelated file was changed", err)
	}
}

func TestUninstallRefusesUnknownBinaryArtifactsBeforeRemovingAnything(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"etc/systemd/system/ozal.service", "usr/local/lib/ozal/ozal", "usr/local/lib/ozal/operator-data"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Uninstall(context.Background(), io.Discard, nil, UninstallRequest{Root: root, Apply: true}); err == nil {
		t.Fatal("accepted an unrecognized binary-directory file")
	}
	for _, path := range []string{"etc/systemd/system/ozal.service", "usr/local/lib/ozal/ozal", "usr/local/lib/ozal/operator-data"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("changed installation before refusing unknown file %s: %v", path, err)
		}
	}
}

type lockingHost struct {
	busy     bool
	locked   []string
	released bool
}

func (h *lockingHost) Systemctl(context.Context, ...string) error { return nil }
func (h *lockingHost) LockUpdates(path string) (func(), error) {
	h.locked = append(h.locked, path)
	if h.busy {
		return nil, errors.New("another update is already running")
	}
	return func() { h.released = true }, nil
}

// Every update leaves update.lock beside the binary. The uninstaller holds
// that lock while removing, and refuses with nothing changed while an update
// owns it.
func TestUninstallHoldsTheUpdateLockAndRefusesARunningUpdate(t *testing.T) {
	stage := func(t *testing.T) string {
		// macOS puts t.TempDir under the /var symlink, which uninstall
		// rightly refuses to traverse.
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"etc/systemd/system/ozal.service", "usr/local/lib/ozal/ozal", "usr/local/lib/ozal/update.lock"} {
			full := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}

	root := stage(t)
	busy := &lockingHost{busy: true}
	err := Uninstall(context.Background(), io.Discard, busy, UninstallRequest{Root: root, Apply: true})
	if err == nil || !strings.Contains(err.Error(), "update is running") {
		t.Fatalf("err = %v, want a refusal naming the running update", err)
	}
	for _, path := range []string{"etc/systemd/system/ozal.service", "usr/local/lib/ozal/ozal", "usr/local/lib/ozal/update.lock"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("changed %s while an update ran: %v", path, err)
		}
	}

	root = stage(t)
	idle := &lockingHost{}
	if err := Uninstall(context.Background(), io.Discard, idle, UninstallRequest{Root: root, Apply: true}); err != nil {
		t.Fatal(err)
	}
	if len(idle.locked) != 1 || idle.locked[0] != filepath.Join(root, "usr/local/lib/ozal/update.lock") || !idle.released {
		t.Fatalf("locked %v released %v; want the update lock held and released", idle.locked, idle.released)
	}
	if _, err := os.Lstat(filepath.Join(root, "usr/local/lib/ozal")); !os.IsNotExist(err) {
		t.Fatalf("binary directory remains: %v", err)
	}
}
