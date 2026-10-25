package pages

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/pages/deployauth"
)

// SourceMetadata records the first nonempty provenance of a content-addressed
// deployment. The HTTP adapter supplies only verified source claims.
type SourceMetadata struct{ Project, CommitSHA, RunURL string }

var ErrProjectConflict = errors.New("artifact belongs to another project")

func (s *Store) DeployProject(ctx context.Context, site string, source SourceMetadata, archive io.Reader) (Deployment, error) {
	if !ValidSiteName(source.Project) || source.CommitSHA != "" && !deployauth.ValidCommit(source.CommitSHA) {
		return Deployment{}, errors.New("invalid project source metadata")
	}
	if source.RunURL != "" {
		u, err := url.Parse(source.RunURL)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return Deployment{}, errors.New("invalid project run URL")
		}
	}
	return s.deploy(ctx, site, source, archive)
}

func (s *Store) Deployment(ctx context.Context, site, id string) (Deployment, error) {
	if !ValidSiteName(site) || len(id) != 64 || strings.ToLower(id) != id {
		return Deployment{}, ErrNotFound
	}
	if _, err := hex.DecodeString(id); err != nil {
		return Deployment{}, ErrNotFound
	}
	d := Deployment{Site: site, ID: id}
	var created int64
	var project, commit, run sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT d.created_at, d.files, d.bytes, d.project, d.commit_sha, d.run_url,
	 EXISTS (SELECT 1 FROM site_live l WHERE l.site = d.site AND l.deployment_id = d.id)
	 FROM deployments d WHERE d.site = ? AND d.id = ?`, site, id).Scan(&created, &d.Files, &d.Bytes, &project, &commit, &run, &d.Live)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, err
	}
	d.CreatedAt = time.Unix(created, 0).UTC()
	d.Project, d.CommitSHA, d.RunURL = project.String, commit.String, run.String
	return d, nil
}

func nullableSource(value string) any {
	if value == "" {
		return nil
	}
	return value
}
