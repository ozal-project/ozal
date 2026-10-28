package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/switchboard-project/switchboard/internal/certs"
)

// DNS is the part of the Cloudflare client a cutover uses.
type DNS interface {
	SiteRecords(ctx context.Context, name string) ([]certs.SiteRecord, error)
	ReplaceSiteRecords(ctx context.Context, name string, remove []certs.SiteRecord, add certs.SiteRecord) error
}

// CutoverRequest moves a live hostname's DNS to the edge, or back.
type CutoverRequest struct {
	Host string
	// To is the record the name should end with: the edge's proxied A
	// record, or with Back the record saved before the cutover.
	To    certs.SiteRecord
	Back  bool
	Apply bool
	// Evidence is the directory where the replaced records are kept.
	Evidence string
	Stamp    string
	// Index, when set, is the build's index.html the edge must serve.
	Index  []byte
	Public *http.Client
	// Answers: how often and how long to wait for the edge to answer.
	Every time.Duration
	Tries int
	// Hint is printed when the edge does not answer after the DNS change,
	// for instance because a Pages custom domain still claims the name.
	Hint      string
	PublicURL string
}

// Cutover replaces the name's address records with To in one batch, waits
// until the edge (not the previous host) answers, checks the build and, on
// any failure or interruption, puts the previous records back.
func Cutover(ctx context.Context, out io.Writer, dns DNS, req CutoverRequest) error {
	step := func(format string, a ...any) { fmt.Fprintf(out, "==> "+format+"\n", a...) }
	note := func(format string, a ...any) { fmt.Fprintf(out, "    "+format+"\n", a...) }
	current, err := dns.SiteRecords(ctx, req.Host)
	if err != nil {
		return err
	}
	for _, r := range current {
		note("now: %s %s proxied=%v", r.Type, r.Content, r.Proxied)
	}
	if len(current) == 1 && current[0].Type == req.To.Type && current[0].Content == req.To.Content && current[0].Proxied == req.To.Proxied {
		step("%s already points at %s: nothing to do", req.Host, req.To.Content)
		return nil
	}
	if !req.Apply {
		step("plan: replace %d record(s) of %s with %s %s (proxied=%v) in one batch", len(current), req.Host, req.To.Type, req.To.Content, req.To.Proxied)
		note("re-run with -apply to do it")
		return nil
	}
	evidence, err := keep(req, current)
	if err != nil {
		return err
	}
	note("previous records kept in %s (sbctl site cutover -back %s restores them)", evidence, evidence)
	step("switch %s to %s", req.Host, req.To.Content)
	if err := dns.ReplaceSiteRecords(ctx, req.Host, current, req.To); err != nil {
		return fmt.Errorf("the DNS batch failed; Cloudflare applies all or nothing, so the records are unchanged: %w", err)
	}
	if req.Back {
		step("%s points at the previous host again", req.Host)
		return nil
	}

	failure := settle(ctx, req, step, note)
	if failure == nil {
		step("%s is served by the edge", req.Host)
		return nil
	}
	step("cutover failed (%v): back to the previous records", failure)
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	now, err := dns.SiteRecords(restoreCtx, req.Host)
	if err == nil && len(current) == 1 {
		err = dns.ReplaceSiteRecords(restoreCtx, req.Host, now, current[0])
	} else if err == nil {
		err = errors.New("more than one previous record: restore them from the evidence file by hand")
	}
	if err != nil {
		return fmt.Errorf("cutover failed (%v) and restoring the previous record failed too (%v); the records are in %s", failure, err, evidence)
	}
	return fmt.Errorf("cutover failed and %s points at the previous host again: %w", req.Host, failure)
}

func settle(ctx context.Context, req CutoverRequest, step func(string, ...any), note func(string, ...any)) error {
	if !edgeAnswers(ctx, req, 20) {
		step("the edge does not answer %s yet", req.Host)
		if req.Hint != "" {
			note("%s", req.Hint)
		}
		note("waiting up to 10 minutes; Ctrl-C goes back to the previous records")
		if !edgeAnswers(ctx, req, 120) {
			if ctx.Err() != nil {
				return errors.New("interrupted")
			}
			return errors.New("the edge never answered")
		}
	}
	note("the edge answers %s", req.Host)
	if len(req.Index) == 0 {
		return nil
	}
	status, body, err := fetch(ctx, req.Public, base(req)+"/")
	if err != nil || status != http.StatusOK || digest(body) != digest(req.Index) {
		return fmt.Errorf("GET / does not serve the expected build (status %d, %v)", status, err)
	}
	note("GET / serves the expected build")
	return nil
}

// edgeAnswers is true once an unknown path answers 404 five times in a row.
// A Pages project answers it with its home page; the edge, with 404. One
// answer is not enough while the change spreads across Cloudflare.
func edgeAnswers(ctx context.Context, req CutoverRequest, tries int) bool {
	every, limit := req.Every, req.Tries
	if every <= 0 {
		every = 3 * time.Second
	}
	if limit > 0 && limit < tries {
		tries = limit
	}
	streak := 0
	for i := 0; i < tries; i++ {
		status, _, err := fetch(ctx, req.Public, fmt.Sprintf("%s/no-such-page-sbctl-check-%d", base(req), i))
		if err == nil && status == http.StatusNotFound {
			streak++
		} else {
			streak = 0
		}
		if streak >= 5 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(every):
		}
	}
	return false
}

func base(req CutoverRequest) string {
	if req.PublicURL != "" {
		return req.PublicURL
	}
	return "https://" + req.Host
}

func keep(req CutoverRequest, records []certs.SiteRecord) (string, error) {
	if err := os.MkdirAll(req.Evidence, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(req.Evidence, "dns-before-"+strings.ReplaceAll(req.Host, ".", "_")+"-"+req.Stamp+".json")
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(data, '\n'), 0o600)
}

// SavedRecord reads the single record a cutover replaced, for -back.
func SavedRecord(path string) (certs.SiteRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return certs.SiteRecord{}, err
	}
	var records []certs.SiteRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return certs.SiteRecord{}, err
	}
	if len(records) != 1 {
		return certs.SiteRecord{}, fmt.Errorf("%s holds %d records; restore them by hand", path, len(records))
	}
	return records[0], nil
}
