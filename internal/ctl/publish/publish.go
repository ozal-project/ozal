// Package publish is the network side of `ozalctl site publish`: it uploads a
// build through an SSH tunnel to the edge's admin API with the sites token and
// accepts it on its public address, rolling back on failure.
package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/ctl"
	"github.com/ozal-project/ozal/internal/ctl/artifact"
	"github.com/ozal-project/ozal/internal/pages"
)

// Tunnel opens a local address forwarded to an address on the edge.
type Tunnel interface {
	Forward(ctx context.Context, remote string) (local string, stop func(), err error)
}

// Request is one run of `ozalctl site publish`: a build the operator
// (or CI) already made is packed, uploaded through an SSH tunnel to the
// edge's admin API with the sites token, and checked on its public address.
// The route itself is the edge's: add the address once in the console.
type Request struct {
	Target ctl.Target
	Site   string
	Dir    string
	// Host, when set, is checked publicly after the upload; a failed check
	// rolls the site back to the build that was live before.
	Host  string
	Token string
	Apply bool
	// PublicURL is where Host is fetched from (default https://Host).
	PublicURL string
	Admin     *http.Client
	Public    *http.Client // must not follow redirects
	Retries   int
	Wait      time.Duration
}

type siteList struct {
	Sites []struct {
		Name string `json:"name"`
		Live string `json:"live"`
	} `json:"sites"`
}

// Run uploads one build and, with a host, accepts it publicly.
func Run(ctx context.Context, out io.Writer, tunnel Tunnel, req Request) error {
	step := func(format string, a ...any) { fmt.Fprintf(out, "==> "+format+"\n", a...) }
	note := func(format string, a ...any) { fmt.Fprintf(out, "    "+format+"\n", a...) }
	if !pages.ValidSiteName(req.Site) {
		return fmt.Errorf("site name %q must be 1-63 characters of a-z, 0-9 or '-'", req.Site)
	}
	if req.Target.Edge.Admin == "" {
		return errors.New("edge.admin is required to reach the admin API through the tunnel")
	}
	step("pack %s", req.Dir)
	snapshot, err := artifact.FromDir(req.Dir)
	if err != nil {
		return err
	}
	archive := snapshot.Archive
	note("%d bytes, sha256 %s…", len(archive), digest(archive)[:16])
	if !req.Apply {
		step("plan: publish %s as the live build of %s%s", req.Dir, req.Site, map[bool]string{true: ", then check " + req.Host, false: ""}[req.Host != ""])
		note("re-run with -apply to do it")
		return nil
	}
	if strings.TrimSpace(req.Token) == "" {
		return errors.New("a sites token is required (edge.sites_token)")
	}

	local, stop, err := tunnel.Forward(ctx, req.Target.Edge.Admin)
	if err != nil {
		return fmt.Errorf("SSH tunnel to the admin listener: %w", err)
	}
	defer stop()
	base := "http://" + local
	var list siteList
	if err := adminCall(ctx, req.Admin, http.MethodGet, base+"/_/api/sites", req.Token, "", nil, &list); err != nil {
		return err
	}
	previous := ""
	for _, s := range list.Sites {
		if s.Name == req.Site {
			previous = s.Live
		}
	}
	step("upload to %s", req.Site)
	var deployment struct {
		ID string `json:"id"`
	}
	if err := adminCall(ctx, req.Admin, http.MethodPost, base+"/_/api/sites/"+url.PathEscape(req.Site)+"/deployments", req.Token, "application/gzip", bytes.NewReader(archive), &deployment); err != nil {
		return err
	}
	note("deployment %s is live (previous: %s)", deployment.ID, orNone(previous))
	if req.Host == "" {
		return nil
	}

	step("check %s", req.Host)
	failure := accept(ctx, req, snapshot.Proof, note)
	if failure == nil {
		step("%s serves the new build", req.Host)
		return nil
	}
	note("%v", failure)
	if previous == "" || previous == deployment.ID {
		return fmt.Errorf("public check failed and there is no earlier build to restore: %w", failure)
	}
	step("roll back %s to %s", req.Site, previous)
	payload, _ := json.Marshal(map[string]string{"id": previous})
	if err := adminCall(ctx, req.Admin, http.MethodPost, base+"/_/api/sites/"+url.PathEscape(req.Site)+"/rollback", req.Token, "application/json", bytes.NewReader(payload), nil); err != nil {
		return fmt.Errorf("public check failed (%v) and the rollback failed too: %w", failure, err)
	}
	return fmt.Errorf("public check failed; %s is back on %s: %w", req.Site, previous, failure)
}

// accept shares the same public proof and refusal checks as CI.
func accept(ctx context.Context, req Request, proof artifact.Proof, note func(string, ...any)) error {
	public := req.PublicURL
	if public == "" {
		public = "https://" + req.Host
	}
	err := Accept(ctx, io.Discard, Acceptance{URL: public, Proof: proof, Client: req.Public, Attempts: req.Retries, Wait: req.Wait})
	if err == nil {
		note("public build proof, hidden paths and HTTPS redirect checked")
	}
	return err
}

func fetch(ctx context.Context, client *http.Client, target string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	return res.StatusCode, body, err
}

func adminCall(ctx context.Context, client *http.Client, method, target, token, contentType string, body io.Reader, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, req.URL.Path, err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(payload, &problem)
		if problem.Error == "" {
			problem.Error = http.StatusText(res.StatusCode)
		}
		return fmt.Errorf("%s %s: %d %s", method, req.URL.Path, res.StatusCode, problem.Error)
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(payload, result)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
