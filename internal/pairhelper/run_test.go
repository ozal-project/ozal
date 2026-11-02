package pairhelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/ctl"
)

const testJob = "pair-20261102T173850Z-5399444b627e9f01"

// fakeHost stands in for cp, chown, curl and the installed binary's db
// commands, recording every call so tests can assert what never ran.
type fakeHost struct {
	t        *testing.T
	profile  Profile
	calls    []string
	cpWrites []byte // when set, cp writes these bytes instead of copying
	status   string // curl -w output
	stdin    string
}

func (h *fakeHost) exec(_ context.Context, stdin io.Reader, stdout io.Writer, name string, args ...string) error {
	h.calls = append(h.calls, name+" "+strings.Join(args, " "))
	switch {
	case name == "/usr/bin/cp" && len(args) == 3 && args[0] == "-p":
		data := h.cpWrites
		if data == nil {
			var err error
			if data, err = os.ReadFile(args[1]); err != nil {
				return err
			}
		}
		return os.WriteFile(args[2], data, 0o755)
	case name == h.profile.Binary && len(args) == 6 && args[0] == "db" && args[1] == "backup":
		data, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		return os.WriteFile(args[5], data, 0o600)
	case name == h.profile.Binary && len(args) == 6 && args[0] == "db" && args[1] == "restore":
		data, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		return os.WriteFile(args[5], data, 0o600)
	case name == "/usr/bin/chown":
		return nil
	case name == "/usr/bin/curl":
		if stdin != nil {
			b, _ := io.ReadAll(stdin)
			h.stdin = string(b)
		}
		_, err := io.WriteString(stdout, h.status)
		return err
	}
	h.t.Fatalf("unexpected command %s %v", name, args)
	return nil
}

func testRunner(t *testing.T) (Runner, *fakeHost) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"bin", "etc", "state", "stage"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p := Profile{Unit: "ozal-edge.service", ServiceUser: "ozal", ServiceGroup: "ozal",
		Binary: filepath.Join(dir, "bin", "ozal"), Config: filepath.Join(dir, "etc", "config.json"),
		StateDB: filepath.Join(dir, "state", "state.db"), Stage: filepath.Join(dir, "stage"),
		Socket: filepath.Join(dir, "admin.sock"), Peer: "portfolio", Keep: 3}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	write(t, p.Binary, "old binary")
	write(t, p.StateDB, "old state")
	host := &fakeHost{t: t, profile: p}
	return Runner{Profile: p, Stdout: io.Discard, Stderr: io.Discard, exec: host.exec}, host
}

func write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCheckpointCopiesBinaryAndDatabase(t *testing.T) {
	r, _ := testRunner(t)
	err := r.checkpoint(context.Background(), Request{Action: "checkpoint", Job: testJob, SHA256: digest([]byte("old binary"))})
	if err != nil {
		t.Fatalf("checkpoint() error = %v", err)
	}
	if got := read(t, r.Profile.Binary+".pair-"+testJob+".bak"); got != "old binary" {
		t.Errorf("binary checkpoint = %q", got)
	}
	if got := read(t, r.Profile.StateDB+".pair-"+testJob+".bak.db"); got != "old state" {
		t.Errorf("database checkpoint = %q", got)
	}
}

func TestCheckpointRefusesAnExistingBackup(t *testing.T) {
	r, host := testRunner(t)
	backup := r.Profile.Binary + ".pair-" + testJob + ".bak"
	write(t, backup, "earlier checkpoint")
	err := r.checkpoint(context.Background(), Request{Action: "checkpoint", Job: testJob, SHA256: digest([]byte("old binary"))})
	if err == nil {
		t.Fatal("checkpoint() overwrote an existing backup")
	}
	if len(host.calls) != 0 || read(t, backup) != "earlier checkpoint" {
		t.Fatalf("checkpoint() touched the existing backup: calls %v", host.calls)
	}
}

func TestCheckpointDiscardsACopyWithTheWrongHash(t *testing.T) {
	r, host := testRunner(t)
	host.cpWrites = []byte("tampered")
	err := r.checkpoint(context.Background(), Request{Action: "checkpoint", Job: testJob, SHA256: digest([]byte("old binary"))})
	if err == nil {
		t.Fatal("checkpoint() accepted a copy that differs from the installed binary")
	}
	if _, statErr := os.Lstat(r.Profile.Binary + ".pair-" + testJob + ".bak"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("mismatched checkpoint was left behind: %v", statErr)
	}
	for _, call := range host.calls {
		if strings.Contains(call, " db backup ") {
			t.Fatal("database checkpoint ran after the binary checkpoint failed")
		}
	}
}

func TestRestoreReturnsBinaryAndDatabaseToTheCheckpoint(t *testing.T) {
	r, _ := testRunner(t)
	p := r.Profile
	write(t, p.Binary+".pair-"+testJob+".bak", "old binary")
	write(t, p.StateDB+".pair-"+testJob+".bak.db", "checkpoint state")
	write(t, p.Binary, "candidate binary")
	write(t, p.StateDB, "candidate state")
	write(t, p.StateDB+"-wal", "candidate wal")
	if err := r.restore(context.Background(), Request{Action: "restore", Job: testJob}); err != nil {
		t.Fatalf("restore() error = %v", err)
	}
	if got := read(t, p.Binary); got != "old binary" {
		t.Errorf("binary after restore = %q", got)
	}
	if got := read(t, p.StateDB); got != "checkpoint state" {
		t.Errorf("database after restore = %q", got)
	}
	if got := read(t, p.StateDB+".failed-"+testJob); got != "candidate state" {
		t.Errorf("candidate database was not kept aside: %q", got)
	}
	if _, err := os.Lstat(p.StateDB + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("candidate WAL survived the restore: %v", err)
	}
}

func TestRestoreRefusesASymlinkedCheckpoint(t *testing.T) {
	for _, which := range []string{"binary", "database"} {
		t.Run(which, func(t *testing.T) {
			r, host := testRunner(t)
			p := r.Profile
			decoy := filepath.Join(filepath.Dir(p.Stage), "decoy")
			write(t, decoy, "attacker bytes")
			binaryBackup, dbBackup := p.Binary+".pair-"+testJob+".bak", p.StateDB+".pair-"+testJob+".bak.db"
			write(t, binaryBackup, "old binary")
			write(t, dbBackup, "checkpoint state")
			link := binaryBackup
			if which == "database" {
				link = dbBackup
			}
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(decoy, link); err != nil {
				t.Fatal(err)
			}
			write(t, p.Binary, "candidate binary")
			if err := r.restore(context.Background(), Request{Action: "restore", Job: testJob}); err == nil {
				t.Fatal("restore() followed a symlinked checkpoint")
			}
			if len(host.calls) != 0 || read(t, p.Binary) != "candidate binary" {
				t.Fatalf("restore() acted on a symlinked checkpoint: calls %v", host.calls)
			}
		})
	}
}

func TestReportSendsProgressOnStdinAndRequiresAcceptanceAfterStart(t *testing.T) {
	progress := func(phase, result string) []byte {
		b, err := json.Marshal(ctl.PairProgress{Phase: phase, Result: result, JobID: testJob})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	tests := []struct {
		name, status, phase, result string
		wantErr                     bool
	}{
		{"accepted after start", "202", "verifying", "updating", false},
		{"legacy 405 while staging", "405", "staging", "updating", false},
		{"legacy 404 before stopping", "404", "stopping", "updating", false},
		{"405 after candidate start", "405", "verifying", "updating", true},
		{"404 after rollback", "404", "complete", "rolled_back", true},
		{"server error", "500", "staging", "updating", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, host := testRunner(t)
			host.status = tt.status
			payload := progress(tt.phase, tt.result)
			err := r.report(context.Background(), payload)
			if (err != nil) != tt.wantErr {
				t.Fatalf("report() error = %v, want error %v", err, tt.wantErr)
			}
			if host.stdin != string(payload) {
				t.Fatalf("progress was not sent on stdin: %q", host.stdin)
			}
		})
	}
}
