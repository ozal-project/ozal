package updatejob

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newJob(t *testing.T, store Store) Job {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	store.Now = func() time.Time { return now }
	j := Job{ID: id, Channel: "stable", From: "0.1.0", To: "0.2.0", Phase: PhaseReviewing, Result: ResultPending}
	if err := store.Create(j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestJobStorePersistsPhasesAndNeverReopensAFinishedJob(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "jobs")}
	j := newJob(t, store)
	if _, err := store.Advance(j.ID, PhaseStaging, "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := store.Advance(j.ID, PhaseInstalling, "", "")
	if err != nil || got.Phase != PhaseInstalling || got.Result != ResultPending {
		t.Fatalf("advance: %+v %v", got, err)
	}
	done, err := store.Advance(j.ID, PhaseComplete, ResultUpdated, "0.1.0 -> 0.2.0")
	if err != nil || !done.Terminal() || done.FinishedAt == "" {
		t.Fatalf("complete: %+v %v", done, err)
	}
	late, err := store.Advance(j.ID, PhaseStaging, "", "late line")
	if err != nil || late.Result != ResultUpdated || late.Phase != PhaseComplete {
		t.Fatalf("late advance reopened: %+v %v", late, err)
	}
	if latest, err := store.Latest(); err != nil || latest.ID != j.ID {
		t.Fatalf("latest %+v %v", latest, err)
	}
}

func TestJobStoreRefusesUnknownFieldsAndSymlinks(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	j := newJob(t, store)
	path, _ := store.path(j.ID)
	raw, _ := os.ReadFile(path)
	raw = []byte(strings.Replace(string(raw), `"channel": "stable"`, `"channel": "stable", "command": "reboot"`, 1))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(j.ID); err == nil {
		t.Fatal("unknown job field accepted")
	}

	other := newJob(t, store)
	link := filepath.Join(store.Dir, strings.Repeat("a", 32)+".json")
	if err := os.Symlink(filepath.Join(store.Dir, other.ID+".json"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(strings.Repeat("a", 32)); err == nil {
		t.Fatal("symlinked job accepted")
	}
}

func TestJobStoreIgnoresBrokenFilesWhenListing(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	j := newJob(t, store)
	if err := os.WriteFile(filepath.Join(store.Dir, strings.Repeat("b", 32)+".json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	latest, err := store.Latest()
	if err != nil || latest.ID != j.ID {
		t.Fatalf("latest %+v %v", latest, err)
	}
}

func TestJobStoreListsOnlyPendingJobsForRecovery(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	first := newJob(t, store)
	second := newJob(t, store)
	if _, err := store.Advance(first.ID, PhaseComplete, ResultUpdated, ""); err != nil {
		t.Fatal(err)
	}
	pending, err := store.Pending()
	if err != nil || len(pending) != 1 || pending[0].ID != second.ID {
		t.Fatalf("pending %+v %v", pending, err)
	}
}

func TestJobValidationBoundsReasonAndVersions(t *testing.T) {
	j := Job{ID: strings.Repeat("a", 32), Channel: "stable", From: "dev", To: "v0.2.0", Phase: PhaseReviewing, Result: ResultPending, StartedAt: "2026-10-30T00:00:00Z", UpdatedAt: "2026-10-30T00:00:00Z"}
	if err := j.Validate(); err != nil {
		t.Fatal(err)
	}
	j.Reason = strings.Repeat("x", 513)
	if err := j.Validate(); err == nil {
		t.Fatal("oversized reason accepted")
	}
	j.Reason = "line\nbreak"
	if err := j.Validate(); err == nil {
		t.Fatal("control character accepted")
	}
	j.Result = ResultUpdated
	if err := j.Validate(); err == nil {
		t.Fatal("finished job without complete phase or finish time accepted")
	}
}

func TestPlanBindsVersionHashPlatformAndReviewTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.json")
	plan := Plan{
		ID: strings.Repeat("a", 32), CheckedAt: "2026-10-31T12:00:00Z",
		Installed: "0.1.0", Latest: "0.2.0", Channel: "stable",
		Platform: "linux/arm64", Result: "available", Reason: "0.2.0 is newer",
		ArchiveSHA256: strings.Repeat("b", 64), Size: 1234, Migrations: true,
		NotesURL: "https://ozal.dev/releases/v0.2.0",
	}
	if err := WritePlan(path, plan); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPlan(path)
	if err != nil || got.Latest != "0.2.0" || got.Platform != "linux/arm64" || !got.Migrations {
		t.Fatalf("plan %+v %v", got, err)
	}
	checked, _ := time.Parse(time.RFC3339, plan.CheckedAt)
	if plan.Expired(checked.Add(ReviewTTL - time.Second)) {
		t.Fatal("fresh plan reported expired")
	}
	if !plan.Expired(checked.Add(ReviewTTL + time.Second)) {
		t.Fatal("old plan accepted")
	}
	if !plan.Expired(checked.Add(-2 * time.Minute)) {
		t.Fatal("future-dated plan accepted")
	}
}

func TestPlanRefusesLinksAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.json")
	plan := Plan{ID: strings.Repeat("a", 32), CheckedAt: "2026-10-31T12:00:00Z", Installed: "0.1.0", Channel: "stable", Platform: "linux/arm64", Result: "current"}
	if err := WritePlan(path, plan); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPlan(link); err == nil {
		t.Fatal("symlinked plan accepted")
	}
	raw, _ := os.ReadFile(path)
	raw = []byte(strings.Replace(string(raw), `"result": "current"`, `"result": "current", "command": "reboot"`, 1))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPlan(path); err == nil {
		t.Fatal("unknown plan field accepted")
	}
}

func TestPlanRefusesANonHTTPSNotesURL(t *testing.T) {
	plan := Plan{ID: strings.Repeat("a", 32), CheckedAt: "2026-10-31T12:00:00Z", Installed: "0.1.0", Channel: "stable", Platform: "linux/arm64", Result: "available", NotesURL: "http://ozal.dev/notes"}
	if err := plan.Validate(); err == nil {
		t.Fatal("plain HTTP notes accepted")
	}
}

func TestDescribePhaseNeverClaimsSuccess(t *testing.T) {
	for _, phase := range []string{PhaseReviewing, PhaseStaging, PhaseVerifying, PhaseInstalling, PhaseStarting} {
		if text := DescribePhase(phase); text == "" || strings.Contains(strings.ToLower(text), "complete") {
			t.Fatalf("phase %s describes %q", phase, text)
		}
	}
}

func TestNewIDIsRandomAndValid(t *testing.T) {
	a, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !idText.MatchString(a) || !idText.MatchString(b) {
		t.Fatalf("ids %q %q", a, b)
	}
}

func TestJobStoreCreateRefusesDuplicateID(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	j := newJob(t, store)
	if err := store.Create(j); err == nil {
		t.Fatal("duplicate job replaced an existing record")
	}
}

func TestJobStoreMissingLatestIsNotExist(t *testing.T) {
	if _, err := (Store{Dir: filepath.Join(t.TempDir(), "none")}).Latest(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}
