package upgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeHost stands in for systemd, the service account and the admin
// listener. A binary is a file whose content names its behaviour; the
// database is a file holding its schema version.
type fakeHost struct {
	t        *testing.T
	binary   string
	db       string
	active   bool
	calls    []string
	asSvc    []string
	chowned  []string
	failStop bool
	// What the new binary does.
	badConfig   bool
	failMigrate bool // migrates halfway, then fails
	pending     bool
}

func (h *fakeHost) Systemctl(_ context.Context, args ...string) error {
	h.calls = append(h.calls, "systemctl "+strings.Join(args, " "))
	switch args[0] {
	case "stop":
		if h.failStop {
			return errors.New("stop failed")
		}
		h.active = false
	case "start":
		h.active = !strings.HasPrefix(h.running(), "broken")
		if !h.active {
			return errors.New("start failed")
		}
	}
	return nil
}

func (h *fakeHost) running() string {
	body, err := os.ReadFile(h.binary)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(body)
}

func (h *fakeHost) Healthy(context.Context) bool {
	return h.active && h.running() != "no-admin"
}

func (h *fakeHost) Run(_ context.Context, asService bool, name string, args ...string) error {
	call := filepath.Base(name) + " " + strings.Join(args, " ")
	h.calls = append(h.calls, call)
	if asService {
		h.asSvc = append(h.asSvc, call)
	}
	switch args[0] {
	case "validate":
		if h.badConfig {
			return errors.New("config refused")
		}
	case "migrate":
		if !h.pending {
			return nil
		}
		backup := args[len(args)-1]
		body, _ := os.ReadFile(h.db)
		if err := os.WriteFile(backup, body, 0o600); err != nil {
			return err
		}
		os.WriteFile(h.db, []byte("v6"), 0o600)
		os.WriteFile(h.db+"-wal", []byte("wal of v6"), 0o600)
		if h.failMigrate {
			return errors.New("migration 7 failed")
		}
		os.WriteFile(h.db, []byte("v7"), 0o600)
	}
	return nil
}

func (h *fakeHost) Chown(path string) error {
	h.chowned = append(h.chowned, filepath.Base(path))
	return nil
}

type scene struct {
	host *fakeHost
	plan Plan
	dir  string
	out  bytes.Buffer
}

func newScene(t *testing.T, newBinary string) *scene {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	binary, source, db := write("ozal", "old"), write("staged", newBinary), write("state.db", "v5")
	sum := sha256.Sum256([]byte(newBinary))
	s := &scene{dir: dir, host: &fakeHost{t: t, binary: binary, db: db, active: true}}
	s.plan = Plan{
		Unit: "ozal", Binary: binary, Source: source, SHA256: hex.EncodeToString(sum[:]),
		Config: "/etc/ozal/config.json", DB: db, Keep: 3,
		Poll: time.Microsecond, Attempts: 3,
		Now: func() time.Time { return time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC) },
	}
	return s
}

func (s *scene) run(t *testing.T) (Result, error) {
	t.Helper()
	return Run(context.Background(), s.plan, s.host, &s.out)
}

func (s *scene) read(name string) string {
	body, _ := os.ReadFile(filepath.Join(s.dir, name))
	return string(body)
}

func (s *scene) stopped() bool { return slices.Contains(s.host.calls, "systemctl stop ozal") }

func TestUpgradeMigratesSwapsAndKeepsThePrevious(t *testing.T) {
	s := newScene(t, "new")
	s.host.pending = true
	if res, err := s.run(t); res != Upgraded || err != nil {
		t.Fatalf("result %v, %v\n%s", res, err, s.out.String())
	}
	if s.host.running() != "new" || s.read("state.db") != "v7" {
		t.Fatalf("running %q, db %q", s.host.running(), s.read("state.db"))
	}
	bak, _ := filepath.Glob(s.plan.Binary + ".bak-*")
	if len(bak) != 1 || s.read(filepath.Base(bak[0])) != "old" {
		t.Fatalf("previous binary not kept: %v", bak)
	}
	if s.read("state.db.pre-"+s.plan.SHA256[:12]+"-20261022T030000Z") != "v5" {
		t.Fatal("database before the migrations not kept")
	}
	if len(s.host.asSvc) != 1 || !strings.HasPrefix(s.host.asSvc[0], "ozal.next migrate up") {
		t.Fatalf("the database must be touched only as the service account: %v", s.host.asSvc)
	}
}

func TestCoordinatedUpgradeMigratesAndSwapsButLeavesTheUnitStopped(t *testing.T) {
	s := newScene(t, "new")
	s.host.pending = true
	s.plan.DeferStart = true
	if result, err := s.run(t); result != Upgraded || err != nil {
		t.Fatalf("deferred result %v, %v\n%s", result, err, s.out.String())
	}
	if s.host.running() != "new" || s.read("state.db") != "v7" || s.host.active {
		t.Fatalf("candidate=%q database=%q active=%v", s.host.running(), s.read("state.db"), s.host.active)
	}
	if slices.Contains(s.host.calls, "systemctl start ozal") {
		t.Fatalf("deferred upgrade started before its peer was installed: %v", s.host.calls)
	}
	if len(s.host.asSvc) != 1 || !strings.Contains(s.host.asSvc[0], "migrate up") {
		t.Fatalf("migration did not run as the service account: %v", s.host.asSvc)
	}
}

// The source belongs to whoever staged it: only root's checked copy runs.
func TestASourceThatIsNotTheBuiltBinaryIsRefusedBeforeTheStop(t *testing.T) {
	s := newScene(t, "new")
	s.plan.SHA256 = strings.Repeat("0", 64)
	if res, _ := s.run(t); res != Refused {
		t.Fatalf("result %v", res)
	}
	if s.stopped() || len(s.host.calls) != 0 {
		t.Fatalf("ran %v before checking the hash", s.host.calls)
	}
	if _, err := os.Stat(s.plan.Binary + ".next"); !os.IsNotExist(err) {
		t.Fatal("unchecked copy left behind")
	}
}

func TestValidationRunsTheCheckedCopyAndARefusedConfigStopsNothing(t *testing.T) {
	s := newScene(t, "new")
	s.host.badConfig = true
	if res, _ := s.run(t); res != Refused || s.stopped() {
		t.Fatalf("result %v, stopped %v", res, s.stopped())
	}
	if s.host.calls[0] != "ozal.next validate -config /etc/ozal/config.json" {
		t.Fatalf("validated with %q, want root's copy", s.host.calls[0])
	}
}

func TestAMigrationFailingHalfwayPutsTheDatabaseBack(t *testing.T) {
	s := newScene(t, "new")
	s.host.pending, s.host.failMigrate = true, true
	if res, _ := s.run(t); res != RolledBack {
		t.Fatalf("result %v\n%s", res, s.out.String())
	}
	if s.read("state.db") != "v5" || s.host.running() != "old" || !s.host.active {
		t.Fatalf("db %q running %q active %v", s.read("state.db"), s.host.running(), s.host.active)
	}
	if _, err := os.Stat(s.host.db + "-wal"); !os.IsNotExist(err) {
		t.Fatal("the migrated database's WAL would replay onto the restored one")
	}
	if len(s.host.chowned) != 1 || s.host.chowned[0] != "state.db" {
		t.Fatalf("restored database not handed to the service account: %v", s.host.chowned)
	}
}

func TestABinaryThatDoesNotStartIsReplacedAndTheSchemaGoesBackToo(t *testing.T) {
	s := newScene(t, "broken")
	s.host.pending = true
	if res, _ := s.run(t); res != RolledBack {
		t.Fatalf("result %v", res)
	}
	if s.host.running() != "old" || s.read("state.db") != "v5" {
		t.Fatalf("running %q db %q", s.host.running(), s.read("state.db"))
	}
}

func TestActiveIsNotEnoughTheAdminMustAnswer(t *testing.T) {
	s := newScene(t, "no-admin")
	if res, _ := s.run(t); res != RolledBack || s.host.running() != "old" {
		t.Fatalf("result %v running %q", res, s.host.running())
	}
}

func TestWithoutPendingMigrationsTheDatabaseIsNotTouched(t *testing.T) {
	s := newScene(t, "broken")
	if res, _ := s.run(t); res != RolledBack {
		t.Fatalf("result %v", res)
	}
	if backups, _ := filepath.Glob(s.host.db + ".pre-*"); len(backups) != 0 || len(s.host.chowned) != 0 {
		t.Fatalf("database touched: %v %v", backups, s.host.chowned)
	}
}

func TestAPreviousVersionThatDoesNotComeBackIsReportedDown(t *testing.T) {
	s := newScene(t, "broken")
	os.WriteFile(s.plan.Binary, []byte("broken-too"), 0o755) // the installed one cannot start either
	if res, _ := s.run(t); res != Down {
		t.Fatalf("result %v", res)
	}
}

func TestAlreadyRunningThisBinaryDoesNothing(t *testing.T) {
	s := newScene(t, "old")
	if res, err := s.run(t); res != Upgraded || err != nil || len(s.host.calls) != 0 {
		t.Fatalf("result %v %v calls %v", res, err, s.host.calls)
	}
}

func TestACancelledCallerBeforeTheStopChangesNothing(t *testing.T) {
	s := newScene(t, "new")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res, _ := Run(ctx, s.plan, s.host, &s.out); res != Refused || s.stopped() {
		t.Fatalf("result %v stopped %v", res, s.stopped())
	}
}

func TestKeepsTheNewestPreviousBinariesOnly(t *testing.T) {
	s := newScene(t, "new")
	for i, name := range []string{"aaaa", "bbbb", "cccc", "dddd"} {
		path := s.plan.Binary + ".bak-" + name
		os.WriteFile(path, []byte("x"), 0o755)
		stamp := time.Now().Add(time.Duration(i-10) * time.Hour)
		os.Chtimes(path, stamp, stamp)
	}
	if res, err := s.run(t); res != Upgraded {
		t.Fatalf("result %v %v", res, err)
	}
	left, _ := filepath.Glob(s.plan.Binary + ".bak-*")
	names := []string{}
	for _, l := range left {
		names = append(names, strings.TrimPrefix(filepath.Base(l), "ozal.bak-"))
	}
	want := []string{bakOf("old"), "cccc", "dddd"}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("kept %v, want %v", names, want)
	}
}

func bakOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:12]
}
