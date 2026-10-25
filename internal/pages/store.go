package pages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrBadSiteName: site names become directory names, so they are
	// restricted to DNS-label characters.
	ErrBadSiteName = errors.New("site name must be 1-63 characters of a-z, 0-9 or '-' with alphanumeric ends")
	// ErrNotFound: no retained deployment with that id for that site.
	ErrNotFound = errors.New("no retained deployment with that id")
)

var siteNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidSiteName reports whether name is usable as a site.
func ValidSiteName(name string) bool { return siteNamePattern.MatchString(name) }

// DefaultKeep is the total number of deployments retained per site, including
// the live deployment. The live deployment is always kept.
const DefaultKeep = 5

// Deployment is one stored build of a site.
type Deployment struct {
	Site      string    `json:"site"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Live      bool      `json:"live"`
	// Source metadata is stored for deployment provenance but is not part of
	// the unauthenticated local site-list response. A future endpoint can
	// expose it through an explicit authenticated view.
	Project   string `json:"-"`
	CommitSHA string `json:"-"`
	RunURL    string `json:"-"`
}

// Site is a site with its retained deployments, newest first.
type Site struct {
	Name        string       `json:"name"`
	Live        string       `json:"live"`
	ChangedAt   time.Time    `json:"changed_at"`
	Deployments []Deployment `json:"deployments"`
}

// Live is what a site serves right now: a deployment directory and the
// rules parsed from its _redirects and _headers. Both change together.
type Live struct {
	Dir   string
	Rules *Rules
}

// Store keeps deployments on disk under dir and their history in SQLite.
// The live directory of every site is also held in an atomic pointer, so the
// request path reads it without touching the database or the disk.
type Store struct {
	dir   string
	db    *sql.DB
	keep  int
	limit Limits
	now   func() time.Time

	mu   sync.Mutex // serialises deploys and rollbacks
	live atomic.Pointer[map[string]Live]

	// afterCommit, when set by a test, runs right after a deploy or rollback
	// commits: nothing from there on may fail the request (BUG-012).
	afterCommit func()
}

// NewStore opens the store and loads which deployment each site serves.
func NewStore(ctx context.Context, db *sql.DB, dir string, keep int, limit Limits) (*Store, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("pages directory %q must be absolute", dir)
	}
	if keep < 1 {
		keep = DefaultKeep
	}
	if err := os.MkdirAll(filepath.Join(dir, "sites"), 0o755); err != nil {
		return nil, fmt.Errorf("pages directory: %w", err)
	}
	s := &Store{dir: dir, db: db, keep: keep, limit: limit, now: time.Now}
	rows, err := db.QueryContext(ctx, `SELECT site, deployment_id FROM site_live`)
	if err != nil {
		return nil, fmt.Errorf("load live deployments: %w", err)
	}
	defer rows.Close()
	live := map[string]Live{}
	for rows.Next() {
		var site, id string
		if err := rows.Scan(&site, &id); err != nil {
			return nil, fmt.Errorf("load live deployments: %w", err)
		}
		live[site] = Live{Dir: s.deploymentDir(site, id)}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load live deployments: %w", err)
	}
	// Rules were checked when each deployment was uploaded; one that no
	// longer parses (a stricter binary) fails the start instead of serving
	// the site without its headers.
	for site, l := range live {
		rules, err := LoadRules(l.Dir)
		if err != nil {
			return nil, fmt.Errorf("site %s: %w", site, err)
		}
		live[site] = Live{Dir: l.Dir, Rules: rules}
	}
	s.live.Store(&live)
	return s, nil
}

// Limits returns the per-deployment limits the store enforces.
func (s *Store) Limits() Limits { return s.limit }

// Live returns what a site currently serves.
func (s *Store) Live(site string) (Live, bool) {
	l, ok := (*s.live.Load())[site]
	return l, ok
}

func (s *Store) siteDir(site string) string { return filepath.Join(s.dir, "sites", site) }

func (s *Store) deploymentDir(site, id string) string { return filepath.Join(s.siteDir(site), id) }

// Deploy extracts a tar.gz into a new deployment and makes it live. A request
// never sees it half-written: files land in a staging directory that becomes
// the deployment with one rename, and the switch is a database update
// followed by one pointer swap. Uploading content that already exists reuses
// its directory and only switches.
func (s *Store) Deploy(ctx context.Context, site string, archive io.Reader) (Deployment, error) {
	return s.deploy(ctx, site, SourceMetadata{}, archive)
}

func (s *Store) deploy(ctx context.Context, site string, source SourceMetadata, archive io.Reader) (Deployment, error) {
	if !ValidSiteName(site) {
		return Deployment{}, ErrBadSiteName
	}
	if err := os.MkdirAll(s.siteDir(site), 0o755); err != nil {
		return Deployment{}, fmt.Errorf("site directory: %w", err)
	}
	// Extraction runs outside the lock: it is the slow part and touches only
	// its own staging directory.
	staging, err := os.MkdirTemp(s.siteDir(site), ".staging-")
	if err != nil {
		return Deployment{}, fmt.Errorf("staging directory: %w", err)
	}
	defer os.RemoveAll(staging) // a no-op once renamed
	manifest, err := Extract(archive, staging, s.limit)
	if err != nil {
		return Deployment{}, err
	}
	id := ManifestHash(manifest.Files)
	rules, err := LoadRules(staging)
	if err != nil {
		return Deployment{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	final := s.deploymentDir(site, id)
	if _, err := os.Stat(final); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(staging, final); err != nil {
			return Deployment{}, fmt.Errorf("activate deployment directory: %w", err)
		}
		syncDir(s.siteDir(site))
	} else if err != nil {
		return Deployment{}, fmt.Errorf("deployment directory: %w", err)
	}

	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Deployment{}, fmt.Errorf("record deployment: %w", err)
	}
	defer tx.Rollback()
	// A repeated upload keeps its first created_at but becomes live again.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO deployments (site, id, created_at, files, bytes, project, commit_sha, run_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (site, id) DO UPDATE SET
		 project = COALESCE(deployments.project, excluded.project),
		 commit_sha = COALESCE(deployments.commit_sha, excluded.commit_sha),
		 run_url = COALESCE(deployments.run_url, excluded.run_url)`,
		site, id, now.Unix(), len(manifest.Files), manifest.Bytes, nullableSource(source.Project), nullableSource(source.CommitSHA), nullableSource(source.RunURL)); err != nil {
		return Deployment{}, fmt.Errorf("record deployment: %w", err)
	}
	if err := setLive(ctx, tx, site, id, now); err != nil {
		return Deployment{}, err
	}
	deployment, err := readDeployment(ctx, tx, site, id)
	if err != nil {
		return Deployment{}, err
	}
	if source.Project != "" && deployment.Project != source.Project {
		return Deployment{}, ErrProjectConflict
	}
	if err := tx.Commit(); err != nil {
		return Deployment{}, fmt.Errorf("record deployment: %w", err)
	}
	s.committed()
	s.swap(site, Live{Dir: final, Rules: rules})
	s.prune(context.WithoutCancel(ctx), site, id)
	return deployment, nil
}

// Rollback makes a retained deployment live again.
func (s *Store) Rollback(ctx context.Context, site, id string) (Deployment, error) {
	if !ValidSiteName(site) {
		return Deployment{}, ErrBadSiteName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM deployments WHERE site = ? AND id = ?`, site, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("find deployment: %w", err)
	}
	dir := s.deploymentDir(site, id)
	if info, err := os.Stat(filepath.Join(dir, "index.html")); err != nil || info.IsDir() {
		return Deployment{}, fmt.Errorf("deployment %s files are missing", id)
	}
	rules, err := LoadRules(dir)
	if err != nil {
		return Deployment{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Deployment{}, fmt.Errorf("rollback: %w", err)
	}
	defer tx.Rollback()
	if err := setLive(ctx, tx, site, id, s.now()); err != nil {
		return Deployment{}, err
	}
	deployment, err := readDeployment(ctx, tx, site, id)
	if err != nil {
		return Deployment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Deployment{}, fmt.Errorf("rollback: %w", err)
	}
	s.committed()
	s.swap(site, Live{Dir: dir, Rules: rules})
	return deployment, nil
}

// Sites lists every site with its retained deployments, newest first. Ties
// within one second are broken by insertion order (rowid), never by id.
func (s *Store) Sites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.site, d.id, d.created_at, d.files, d.bytes, l.deployment_id, l.changed_at,
		       d.project, d.commit_sha, d.run_url
		 FROM deployments d LEFT JOIN site_live l ON l.site = d.site
		 ORDER BY d.site, d.created_at DESC, d.rowid DESC`)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()
	var sites []Site
	for rows.Next() {
		var d Deployment
		var created int64
		var live sql.NullString
		var changed sql.NullInt64
		var project, commitSHA, runURL sql.NullString
		if err := rows.Scan(&d.Site, &d.ID, &created, &d.Files, &d.Bytes, &live, &changed, &project, &commitSHA, &runURL); err != nil {
			return nil, fmt.Errorf("list deployments: %w", err)
		}
		d.Project, d.CommitSHA, d.RunURL = project.String, commitSHA.String, runURL.String
		d.CreatedAt = time.Unix(created, 0).UTC()
		d.Live = live.Valid && live.String == d.ID
		if len(sites) == 0 || sites[len(sites)-1].Name != d.Site {
			sites = append(sites, Site{Name: d.Site, Live: live.String, ChangedAt: time.Unix(changed.Int64, 0).UTC()})
		}
		last := &sites[len(sites)-1]
		last.Deployments = append(last.Deployments, d)
	}
	return sites, rows.Err()
}

// readDeployment reads the deployment being made live inside the same
// transaction, so the answer is known before the commit. Once the commit
// succeeds the build is live: a read after it, under a request context the
// client may already have dropped, could report an error for a switch that
// happened (BUG-012).
func readDeployment(ctx context.Context, tx *sql.Tx, site, id string) (Deployment, error) {
	d := Deployment{Site: site, ID: id, Live: true}
	var created int64
	var project, commitSHA, runURL sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT created_at, files, bytes, project, commit_sha, run_url FROM deployments WHERE site = ? AND id = ?`, site, id).
		Scan(&created, &d.Files, &d.Bytes, &project, &commitSHA, &runURL)
	if err != nil {
		return Deployment{}, fmt.Errorf("read deployment: %w", err)
	}
	d.CreatedAt = time.Unix(created, 0).UTC()
	d.Project, d.CommitSHA, d.RunURL = project.String, commitSHA.String, runURL.String
	return d, nil
}

func (s *Store) committed() {
	if s.afterCommit != nil {
		s.afterCommit()
	}
}

func setLive(ctx context.Context, tx *sql.Tx, site, id string, now time.Time) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO site_live (site, deployment_id, changed_at) VALUES (?, ?, ?)
		 ON CONFLICT (site) DO UPDATE SET deployment_id = excluded.deployment_id, changed_at = excluded.changed_at`,
		site, id, now.Unix())
	if err != nil {
		return fmt.Errorf("switch live deployment: %w", err)
	}
	return nil
}

// swap publishes a new live map: copy, change one entry, store. Readers keep
// whichever complete map they loaded; none ever sees a partial update.
func (s *Store) swap(site string, l Live) {
	current := *s.live.Load()
	next := make(map[string]Live, len(current)+1)
	for k, v := range current {
		next[k] = v
	}
	next[site] = l
	s.live.Store(&next)
}

// prune keeps at most s.keep deployments of a site, including the live one,
// and removes the rest, rows first so a crash never leaves a row without files.
// Failures are left for the next deploy: pruning is housekeeping.
func (s *Store) prune(ctx context.Context, site, live string) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM deployments WHERE site = ? AND id <> ? ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?`,
		site, live, s.keep-1)
	if err != nil {
		return
	}
	var stale []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM deployments WHERE site = ? AND id = ?`, site, id); err != nil {
			continue
		}
		os.RemoveAll(s.deploymentDir(site, id))
	}
}

// syncDir makes a rename durable on Unix. Windows cannot fsync a directory;
// NTFS journals the rename itself.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if f, err := os.Open(dir); err == nil {
		f.Sync()
		f.Close()
	}
}
