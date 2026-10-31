// Package updatejob owns the durable, secret-free record of one update job
// and the plan an operator reviews. It is shared by the CLI, the local
// executor and the console API; it never runs a command or chooses a path.
package updatejob

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// MaxJobBytes and MaxPlanBytes bound what the console side may read.
	MaxJobBytes  = 64 << 10
	MaxPlanBytes = 64 << 10
	// ReviewTTL is how long an operator's review stays actionable. The
	// executor refuses to start a job whose plan is older.
	ReviewTTL = 10 * time.Minute
)

var (
	idText      = regexp.MustCompile(`^[a-f0-9]{32}$`)
	versionText = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$`)
	phaseText   = regexp.MustCompile(`^[a-z_]{3,32}$`)
	resultText  = regexp.MustCompile(`^[a-z_]{3,32}$`)
)

// Phases reported by an update job. They are stable for the console contract.
const (
	PhaseReviewing  = "reviewing"
	PhaseStaging    = "staging"
	PhaseVerifying  = "verifying"
	PhaseInstalling = "installing"
	PhaseStarting   = "starting"
	PhaseComplete   = "complete"
)

// Results. Pending is the only non-terminal result.
const (
	ResultPending              = "pending"
	ResultUpdated              = "updated"
	ResultRolledBack           = "rolled_back"
	ResultFailed               = "failed"
	ResultInterventionRequired = "intervention_required"
)

// Job is one single-host update. Pair jobs keep their own coordinator and are
// not represented here.
type Job struct {
	ID         string `json:"id"`
	Channel    string `json:"channel"`
	From       string `json:"from"`
	To         string `json:"to,omitempty"`
	Phase      string `json:"phase"`
	Result     string `json:"result"`
	Reason     string `json:"reason,omitempty"`
	StartedAt  string `json:"started_at"`
	UpdatedAt  string `json:"updated_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// NewID returns a random, non-guessable job or plan identifier.
func NewID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// ValidID reports whether value is a well-formed job or plan identifier.
func ValidID(value string) bool { return idText.MatchString(value) }

func (j Job) Terminal() bool { return j.Result != ResultPending }

// Validate refuses records that the console or executor could misread.
func (j Job) Validate() error {
	if !idText.MatchString(j.ID) {
		return errors.New("job id is invalid")
	}
	if j.Channel != "stable" && j.Channel != "preview" {
		return errors.New("job channel is invalid")
	}
	if !versionText.MatchString(j.From) || (j.To != "" && !versionText.MatchString(j.To)) {
		return errors.New("job versions are invalid")
	}
	if !validPhase(j.Phase) {
		return errors.New("job phase is invalid")
	}
	if !validResult(j.Result) {
		return errors.New("job result is invalid")
	}
	if len(j.Reason) > 512 || strings.ContainsFunc(j.Reason, func(r rune) bool { return r < 0x20 }) {
		return errors.New("job reason is invalid")
	}
	started, err := time.Parse(time.RFC3339, j.StartedAt)
	if err != nil {
		return errors.New("job started_at must be RFC3339")
	}
	updated, err := time.Parse(time.RFC3339, j.UpdatedAt)
	if err != nil || updated.Before(started) {
		return errors.New("job updated_at must be RFC3339 and not before started_at")
	}
	if j.Result == ResultPending {
		if j.Phase == PhaseComplete || j.FinishedAt != "" {
			return errors.New("a pending job cannot be complete")
		}
		return nil
	}
	if j.Phase != PhaseComplete {
		return errors.New("a finished job must be in the complete phase")
	}
	if _, err := time.Parse(time.RFC3339, j.FinishedAt); err != nil {
		return errors.New("job finished_at must be RFC3339")
	}
	return nil
}

func validPhase(value string) bool {
	if !phaseText.MatchString(value) {
		return false
	}
	switch value {
	case PhaseReviewing, PhaseStaging, PhaseVerifying, PhaseInstalling, PhaseStarting, PhaseComplete:
		return true
	}
	return false
}

func validResult(value string) bool {
	if !resultText.MatchString(value) {
		return false
	}
	switch value {
	case ResultPending, ResultUpdated, ResultRolledBack, ResultFailed, ResultInterventionRequired:
		return true
	}
	return false
}

// Plan is what the operator reviews before installing. It binds the exact
// version, platform and archive the review saw. The executor refuses to start
// when the catalog, version or hash changed in the meantime.
type Plan struct {
	ID            string `json:"id"`
	CheckedAt     string `json:"checked_at"`
	Installed     string `json:"installed"`
	Latest        string `json:"latest,omitempty"`
	Channel       string `json:"channel"`
	Platform      string `json:"platform"`
	Result        string `json:"result"`
	Reason        string `json:"reason,omitempty"`
	ArchiveSHA256 string `json:"archive_sha256,omitempty"`
	Size          int64  `json:"size,omitempty"`
	Migrations    bool   `json:"migrations,omitempty"`
	NotesURL      string `json:"notes_url,omitempty"`
}

func (p Plan) Validate() error {
	if !idText.MatchString(p.ID) {
		return errors.New("plan id is invalid")
	}
	if _, err := time.Parse(time.RFC3339, p.CheckedAt); err != nil {
		return errors.New("plan checked_at must be RFC3339")
	}
	if !versionText.MatchString(p.Installed) || (p.Latest != "" && !versionText.MatchString(p.Latest)) {
		return errors.New("plan versions are invalid")
	}
	if p.Channel != "stable" && p.Channel != "preview" {
		return errors.New("plan channel is invalid")
	}
	if p.Platform == "" || len(p.Platform) > 32 {
		return errors.New("plan platform is invalid")
	}
	if !validPlanResult(p.Result) {
		return errors.New("plan result is invalid")
	}
	if len(p.Reason) > 512 || strings.ContainsFunc(p.Reason, func(r rune) bool { return r < 0x20 }) {
		return errors.New("plan reason is invalid")
	}
	if p.ArchiveSHA256 != "" && !hex64(p.ArchiveSHA256) {
		return errors.New("plan archive_sha256 is invalid")
	}
	if p.Size < 0 {
		return errors.New("plan size is invalid")
	}
	if p.NotesURL != "" && !strings.HasPrefix(p.NotesURL, "https://") {
		return errors.New("plan notes_url must be HTTPS")
	}
	return nil
}

func validPlanResult(value string) bool {
	switch value {
	case "current", "available", "unversioned", "incompatible", "failed":
		return true
	}
	return false
}

func hex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// Store is a directory of one JSON file per job. The directory and files are
// private to the executor's account.
type Store struct {
	Dir string
	Now func() time.Time
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s Store) path(id string) (string, error) {
	if !idText.MatchString(id) {
		return "", errors.New("job id is invalid")
	}
	return filepath.Join(s.Dir, id+".json"), nil
}

// Create writes a new job and refuses to replace an existing one.
func (s Store) Create(j Job) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	if j.StartedAt == "" {
		j.StartedAt = s.now().Format(time.RFC3339)
	}
	if j.UpdatedAt == "" {
		j.UpdatedAt = j.StartedAt
	}
	if j.Phase == "" {
		j.Phase = PhaseReviewing
	}
	if j.Result == "" {
		j.Result = ResultPending
	}
	if err := j.Validate(); err != nil {
		return err
	}
	path, err := s.path(j.ID)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		os.Remove(path)
		return err
	}
	return f.Close()
}

// Write replaces one job atomically. It never creates a new id.
func (s Store) Write(j Job) error {
	if err := j.Validate(); err != nil {
		return err
	}
	path, err := s.path(j.ID)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return errors.New("the job does not exist or is not a regular file")
	}
	raw, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".job-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
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

// Advance records a phase while the job is pending. A finished job is never
// reopened by a late progress line.
func (s Store) Advance(id, phase, result, reason string) (Job, error) {
	j, err := s.Read(id)
	if err != nil {
		return Job{}, err
	}
	if j.Terminal() {
		return j, nil
	}
	if phase != "" {
		if !validPhase(phase) {
			return Job{}, errors.New("job phase is invalid")
		}
		j.Phase = phase
	}
	if result != "" {
		if !validResult(result) {
			return Job{}, errors.New("job result is invalid")
		}
		j.Result = result
		if result != ResultPending {
			j.FinishedAt = s.now().Format(time.RFC3339)
		}
	}
	j.Reason = reason
	j.UpdatedAt = s.now().Format(time.RFC3339)
	return j, s.Write(j)
}

// Read loads one validated job. A symlink or oversized file is refused.
func (s Store) Read(id string) (Job, error) {
	path, err := s.path(id)
	if err != nil {
		return Job{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Job{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxJobBytes {
		return Job{}, errors.New("the job is not a small regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err := strictJSON(raw, &j); err != nil {
		return Job{}, err
	}
	if err := j.Validate(); err != nil {
		return Job{}, err
	}
	return j, nil
}

// Latest returns the most recently started job, or os.ErrNotExist.
func (s Store) Latest() (Job, error) {
	jobs, err := s.list()
	if err != nil {
		return Job{}, err
	}
	if len(jobs) == 0 {
		return Job{}, os.ErrNotExist
	}
	return jobs[0], nil
}

// list returns jobs newest first, skipping names and records that are not
// jobs. The console path must remain available even if an old file is broken.
func (s Store) list() ([]Job, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var jobs []Job
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || !idText.MatchString(strings.TrimSuffix(name, ".json")) {
			continue
		}
		j, err := s.Read(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(i, k int) bool { return jobs[i].StartedAt > jobs[k].StartedAt })
	return jobs, nil
}

// Pending returns every non-terminal job, oldest first. The executor uses it
// at startup to reconcile an interrupted install.
func (s Store) Pending() ([]Job, error) {
	jobs, err := s.list()
	if err != nil {
		return nil, err
	}
	var pending []Job
	for _, j := range jobs {
		if !j.Terminal() {
			pending = append(pending, j)
		}
	}
	sort.Slice(pending, func(i, k int) bool { return pending[i].StartedAt < pending[k].StartedAt })
	return pending, nil
}

func strictJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("more than one JSON document")
	}
	return nil
}

// WritePlan replaces a plan file atomically. It is readable by the service
// account for display; it carries no secret and no executable instruction.
func WritePlan(path string, plan Plan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-plan-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadPlan loads and validates a plan file, refusing links and oversized data.
func ReadPlan(path string) (Plan, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Plan{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxPlanBytes {
		return Plan{}, errors.New("the plan is not a small regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	var p Plan
	if err := strictJSON(raw, &p); err != nil {
		return Plan{}, err
	}
	if err := p.Validate(); err != nil {
		return Plan{}, err
	}
	return p, nil
}

// Expired reports whether the review is too old to apply.
func (p Plan) Expired(now time.Time) bool {
	checked, err := time.Parse(time.RFC3339, p.CheckedAt)
	if err != nil {
		return true
	}
	age := now.Sub(checked)
	return age > ReviewTTL || age < -time.Minute
}

// DescribePhase is the short text the console can show without inventing
// progress. It is stable for tests and translations.
func DescribePhase(phase string) string {
	switch phase {
	case PhaseReviewing:
		return "Reviewing the signed release"
	case PhaseStaging:
		return "Downloading the verified archive"
	case PhaseVerifying:
		return "Checking the archive and the configuration"
	case PhaseInstalling:
		return "Installing and restarting Ozal"
	case PhaseStarting:
		return "Waiting for Ozal to come back"
	case PhaseComplete:
		return "Finished"
	default:
		return "Working"
	}
}

// String is for logs: it does not include the reason, which may name a path.
func (j Job) String() string {
	return fmt.Sprintf("%s (%s/%s)", j.ID, j.Phase, j.Result)
}
