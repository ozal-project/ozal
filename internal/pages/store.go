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

// DefaultKeep is how many deployments per site are retained (the live one is
// always kept in addition).
const DefaultKeep = 5

// Deployment is one stored build of a site.
type Deployment struct {
	Site      string    `json:"site"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Live      bool      `json:"live"`
}

// Site is a site with its retained deployments, newest first.
type Site struct {
	Name        string       `json:"name"`
	Live        string       `json:"live"`
	ChangedAt   time.Time    `json:"changed_at"`
	Deployments []Deployment `json:"deployments"`
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
	live atomic.Pointer[map[string]string]
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
	live := map[string]string{}
	for rows.Next() {
		var site, id string
		if err := rows.Scan(&site, &id); err != nil {
			return nil, fmt.Errorf("load live deployments: %w", err)
		}
		live[site] = s.deploymentDir(site, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load live deployments: %w", err)
	}
	s.live.Store(&live)
	return s, nil
}

// Limits returns the per-deployment limits the store enforces.
func (s *Store) Limits() Limits { return s.limit }

// LiveDir returns the directory a site currently serves.
func (s *Store) LiveDir(site string) (string, bool) {
	dir, ok := (*s.live.Load())[site]
	return dir, ok
}

func (s *Store) siteDir(site string) string { return filepath.Join(s.dir, "sites", site) }

func (s *Store) deploymentDir(site, id string) string { return filepath.Join(s.siteDir(site), id) }

// Deploy extracts a tar.gz into a new deployment and makes it live. A request
// never sees it half-written: files land in a staging directory that becomes
// the deployment with one rename, and the switch is a database update
// followed by one pointer swap. Uploading content that already exists reuses
// its directory and only switches.
func (s *Store) Deploy(ctx context.Context, site string, archive io.Reader) (Deployment, error) {
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
		`INSERT INTO deployments (site, id, created_at, files, bytes) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (site, id) DO NOTHING`,
		site, id, now.Unix(), len(manifest.Files), manifest.Bytes); err != nil {
		return Deployment{}, fmt.Errorf("record deployment: %w", err)
	}
	if err := setLive(ctx, tx, site, id, now); err != nil {
		return Deployment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Deployment{}, fmt.Errorf("record deployment: %w", err)
	}
	s.swap(site, final)
	s.prune(ctx, site, id)
	return s.deployment(ctx, site, id)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Deployment{}, fmt.Errorf("rollback: %w", err)
	}
	defer tx.Rollback()
	if err := setLive(ctx, tx, site, id, s.now()); err != nil {
		return Deployment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Deployment{}, fmt.Errorf("rollback: %w", err)
	}
	s.swap(site, dir)
	return s.deployment(ctx, site, id)
}

// Sites lists every site with its retained deployments, newest first. Ties
// within one second are broken by insertion order (rowid), never by id.
func (s *Store) Sites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.site, d.id, d.created_at, d.files, d.bytes, l.deployment_id, l.changed_at
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
		if err := rows.Scan(&d.Site, &d.ID, &created, &d.Files, &d.Bytes, &live, &changed); err != nil {
			return nil, fmt.Errorf("list deployments: %w", err)
		}
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

func (s *Store) deployment(ctx context.Context, site, id string) (Deployment, error) {
	sites, err := s.Sites(ctx)
	if err != nil {
		return Deployment{}, err
	}
	for _, st := range sites {
		for _, d := range st.Deployments {
			if st.Name == site && d.ID == id {
				return d, nil
			}
		}
	}
	return Deployment{}, ErrNotFound
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
func (s *Store) swap(site, dir string) {
	current := *s.live.Load()
	next := make(map[string]string, len(current)+1)
	for k, v := range current {
		next[k] = v
	}
	next[site] = dir
	s.live.Store(&next)
}

// prune keeps the newest s.keep deployments of a site plus the live one and
// removes the rest, rows first so a crash never leaves a row without files.
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
