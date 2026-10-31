package updateexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ozal-project/ozal/internal/updatejob"
	"github.com/ozal-project/ozal/internal/updatelock"
)

// Runner executes the update subcommand. cmd/ozal provides the real one;
// tests provide a fake, so no test stops a service or needs root.
type Runner interface {
	Run(ctx context.Context, args []string, log io.Writer) error
}

// Server is the local executor. It serializes plan checks and allows one job
// at a time; the job itself continues after the HTTP request is gone.
type Server struct {
	Profile Profile
	Store   updatejob.Store
	Runner  Runner
	Now     func() time.Time

	planMu   sync.Mutex
	plan     *updatejob.Plan
	jobMu    sync.Mutex
	activeID string
	wg       sync.WaitGroup
}

// ErrReviewRequired means no fresh reviewed plan matches the request.
var ErrReviewRequired = errors.New("review a fresh update plan before installing")

// ErrNoUpdate means the reviewed plan has nothing to install.
var ErrNoUpdate = errors.New("the reviewed plan has no update to install")

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /v1/plan", s.handlePlan)
	mux.HandleFunc("POST /v1/jobs", s.handleStart)
	mux.HandleFunc("GET /v1/jobs/latest", s.handleLatest)
	mux.HandleFunc("GET /v1/jobs/{id}", s.handleJob)
	return mux
}

// Recover closes jobs left pending by an interrupted executor. It does not
// claim success or a rollback: without the journal's final record, the host
// needs a person to verify the unit and the version.
func (s *Server) Recover() error {
	// A child updater may still be running after this executor restarted.
	// Its shared lock is the authority; never overwrite its live journal.
	lockPath := s.Profile.Lock
	if lockPath == "" {
		lockPath = filepath.Join(filepath.Dir(s.Profile.Binary), "update.lock")
	}
	probe, err := updatelock.Acquire(lockPath)
	switch {
	case errors.Is(err, updatelock.ErrBusy):
		return nil
	case err == nil:
		_ = probe.Release()
	}
	pending, err := s.Store.Pending()
	if err != nil {
		return err
	}
	for _, j := range pending {
		if _, err := s.Store.Advance(j.ID, updatejob.PhaseComplete, updatejob.ResultInterventionRequired,
			"the update executor restarted while this job was in progress; verify the unit and its version"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.Plan(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2048))
	if err != nil || len(raw) > 1024 {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "the request is too large"})
		return
	}
	var req struct {
		PlanID  string `json:"plan_id"`
		Confirm bool   `json:"confirm"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || !req.Confirm || !updatejob.ValidID(req.PlanID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "confirm a reviewed plan id"})
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send exactly one JSON document"})
		return
	}
	job, err := s.Start(r.Context(), req.PlanID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, job)
	case errors.Is(err, ErrReviewRequired):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrNoUpdate):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the update executor could not start the job"})
	}
}

func (s *Server) handleLatest(w http.ResponseWriter, _ *http.Request) {
	job, err := s.Store.Latest()
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no update job has run"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the update job could not be read"})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !updatejob.ValidID(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such update job"})
		return
	}
	job, err := s.Store.Read(id)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such update job"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the update job could not be read"})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// Plan checks the signed catalog and returns a reviewed plan. Concurrent
// checks are serialized; no archive is downloaded.
func (s *Server) Plan(ctx context.Context) (updatejob.Plan, error) {
	s.planMu.Lock()
	defer s.planMu.Unlock()
	if s.Runner == nil {
		return updatejob.Plan{}, errors.New("the update executor has no release runner")
	}
	if err := os.MkdirAll(s.Profile.Jobs, 0o700); err != nil {
		return updatejob.Plan{}, errors.New("the update executor cannot prepare its job directory")
	}
	planPath := filepath.Join(s.Profile.Jobs, "plan.json")
	_ = os.Remove(planPath)
	log, err := os.OpenFile(filepath.Join(s.Profile.Jobs, "plan.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return updatejob.Plan{}, errors.New("the update executor cannot open its log")
	}
	defer log.Close()
	args := append(s.baseArgs(), "-plan", planPath)
	runErr := s.Runner.Run(ctx, args, log)
	plan, readErr := updatejob.ReadPlan(planPath)
	if readErr != nil {
		if runErr == nil {
			runErr = readErr
		}
		return updatejob.Plan{}, errors.New("the release check did not produce a reviewed plan; see the host log")
	}
	// A plan that names an installable release is only trusted when the check
	// also completed. A failed check may have written the plan before the
	// failure (for example, the rollback state could not be persisted).
	if runErr != nil && plan.Result != "failed" && plan.Result != "incompatible" {
		return updatejob.Plan{}, errors.New("the release check did not complete; try again")
	}
	if plan.Channel != s.Profile.Channel || plan.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		return updatejob.Plan{}, errors.New("the release check produced a plan for another channel or platform")
	}
	// A failed check still has a plan the console may explain; runErr is
	// represented by plan.Result, not as a transport failure.
	s.plan = &plan
	return plan, nil
}

// Start launches the reviewed plan. Repeated requests while one job runs
// return that same job, so a double click or a retried request cannot install
// twice. The updater revalidates the catalog and refuses a changed version or
// archive hash before stopping the service.
func (s *Server) Start(ctx context.Context, planID string) (updatejob.Job, error) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.activeID != "" {
		if job, err := s.Store.Read(s.activeID); err == nil && !job.Terminal() {
			return job, nil
		}
		s.activeID = ""
	}
	s.planMu.Lock()
	plan := s.plan
	s.planMu.Unlock()
	if plan == nil || plan.ID != planID {
		return updatejob.Job{}, ErrReviewRequired
	}
	if plan.Expired(s.now()) {
		return updatejob.Job{}, ErrReviewRequired
	}
	if plan.Result != "available" || plan.Latest == "" || plan.ArchiveSHA256 == "" {
		return updatejob.Job{}, ErrNoUpdate
	}
	id, err := updatejob.NewID()
	if err != nil {
		return updatejob.Job{}, err
	}
	job := updatejob.Job{ID: id, Channel: plan.Channel, From: plan.Installed, To: plan.Latest,
		Phase: updatejob.PhaseStaging, Result: updatejob.ResultPending}
	if err := s.Store.Create(job); err != nil {
		return updatejob.Job{}, err
	}
	// A reviewed plan is consumed by the job. After it finishes, another
	// install needs a new review rather than silently reusing an old one.
	s.planMu.Lock()
	s.plan = nil
	s.planMu.Unlock()
	s.activeID = id
	s.wg.Add(1)
	go s.run(job, *plan)
	if stored, err := s.Store.Read(id); err == nil {
		return stored, nil
	}
	return job, nil
}

func (s *Server) run(job updatejob.Job, plan updatejob.Plan) {
	defer s.wg.Done()
	defer func() {
		s.jobMu.Lock()
		if s.activeID == job.ID {
			s.activeID = ""
		}
		s.jobMu.Unlock()
	}()
	workCtx := context.WithoutCancel(context.Background())
	var log io.Writer
	file, err := os.OpenFile(filepath.Join(s.Profile.Jobs, job.ID+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		defer file.Close()
		log = file
	} else {
		log = io.Discard
	}
	args := append(s.baseArgs(),
		"-jobs", s.Profile.Jobs, "-job", job.ID,
		"-expect", plan.Latest, "-expect-sha256", plan.ArchiveSHA256,
		"-apply")
	runErr := s.Runner.Run(workCtx, args, log)
	stored, readErr := s.Store.Read(job.ID)
	if readErr == nil && !stored.Terminal() {
		result := updatejob.ResultFailed
		var exited interface{ ExitCode() int }
		if errors.As(runErr, &exited) {
			switch exited.ExitCode() {
			case 3:
				result = updatejob.ResultRolledBack
			case 4:
				result = updatejob.ResultInterventionRequired
			}
		}
		_, _ = s.Store.Advance(job.ID, updatejob.PhaseComplete, result,
			"the updater ended without a final record; verify the unit and its version")
	}
}

func (s *Server) baseArgs() []string {
	args := []string{
		"-catalog", s.Profile.CatalogURL(runtime.GOOS, runtime.GOARCH),
		"-channel", s.Profile.Channel,
		"-state", s.Profile.State,
		"-public-key", s.Profile.PublicKey,
		"-unit", s.Profile.Unit,
		"-binary", s.Profile.Binary,
		"-config", s.Profile.Config,
		"-status", s.Profile.Status,
	}
	if s.Profile.DB != "" {
		args = append(args, "-db", s.Profile.DB, "-user", s.Profile.User)
	}
	if s.Profile.Keep > 0 {
		args = append(args, "-keep", strconv.Itoa(s.Profile.Keep))
	}
	if s.Profile.Lock != "" {
		args = append(args, "-lock", s.Profile.Lock)
	}
	return args
}

// Serve creates the private Unix socket and serves it until ctx ends. A
// stale socket is replaced; any other file at the path is refused untouched.
func (s *Server) Serve(ctx context.Context) error {
	if err := s.Profile.Validate(); err != nil {
		return err
	}
	if err := s.Recover(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Profile.Socket), 0o755); err != nil {
		return fmt.Errorf("create the update executor socket directory: %w", err)
	}
	if info, err := os.Lstat(s.Profile.Socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("the update executor socket path holds a file that is not a socket")
		}
		if socketIsLive(s.Profile.Socket) {
			return errors.New("another update executor is already listening on the configured socket")
		}
		if err := os.Remove(s.Profile.Socket); err != nil {
			return fmt.Errorf("remove the stale update executor socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect the update executor socket: %w", err)
	}
	listener, err := net.Listen("unix", s.Profile.Socket)
	if err != nil {
		return fmt.Errorf("listen on the update executor socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(s.Profile.Socket)
	mode := os.FileMode(0o600)
	if s.Profile.SocketGroup != "" {
		mode = 0o660
		group, err := user.LookupGroup(s.Profile.SocketGroup)
		if err != nil {
			return fmt.Errorf("resolve update executor socket group: %w", err)
		}
		gid, err := strconv.Atoi(group.Gid)
		if err != nil {
			return fmt.Errorf("resolve update executor socket group: %w", err)
		}
		if err := os.Chown(s.Profile.Socket, -1, gid); err != nil {
			return fmt.Errorf("own the update executor socket: %w", err)
		}
	}
	if err := os.Chmod(s.Profile.Socket, mode); err != nil {
		return fmt.Errorf("protect the update executor socket: %w", err)
	}
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// A job already running owns the unit's lifecycle until it finishes; the
	// service manager's stop timeout bounds this wait.
	s.wg.Wait()
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// socketIsLive distinguishes a running executor from a stale socket file.
// A listener that accepts but does not answer still counts as live: refusing
// to replace it is safer than stealing its path.
func socketIsLive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
