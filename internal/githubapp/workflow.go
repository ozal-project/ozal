package githubapp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/pages/workflow"
)

var (
	ErrWorkflowExists = errors.New("workflow already exists on the base branch; review it in GitHub or choose another filename")
	ErrBranchExists   = errors.New("the proposal branch already exists; inspect its commits and pull requests in GitHub before retrying")
	ErrUnconfirmed    = errors.New("GitHub write was not confirmed; inspect the proposal branch and pull requests before retrying")
)

type PullRequest struct {
	URL    string `json:"url"`
	Number int64  `json:"number"`
	Branch string `json:"branch"`
}

// OpenWorkflow creates a fresh branch and adds exactly one new workflow. It
// never changes the base branch, replaces an existing file, force-pushes,
// merges a PR or deletes partial work after an uncertain network result.
func (c *Client) OpenWorkflow(ctx context.Context, cfg config.GitHubApp, selected Repository, plan workflow.Plan) (PullRequest, error) {
	checked, err := workflow.Build(plan.Request)
	if err != nil || checked != plan || selected.ID < 1 || selected.Owner.ID < 1 || selected.FullName != plan.Repository || cfg.Validate() != nil {
		return PullRequest{}, ErrUnavailable
	}
	token, err := c.installationToken(ctx, cfg, selected.ID)
	if err != nil {
		return PullRequest{}, ErrUnavailable
	}
	// IDs, owner and name must still match after the operator's selection.
	// An installation-scoped token also refuses a removed repository here.
	var current Repository
	if err := c.call(ctx, http.MethodGet, "/repositories/"+strconv.FormatInt(selected.ID, 10), token, nil, &current); err != nil ||
		current.ID != selected.ID || current.Owner.ID != selected.Owner.ID || current.FullName != selected.FullName || !validFullName(current.FullName) {
		return PullRequest{}, ErrUnavailable
	}
	basePath := "/repos/" + current.FullName
	var base struct {
		Ref    string `json:"ref"`
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := c.call(ctx, http.MethodGet, basePath+"/git/ref/heads/"+url.PathEscape(plan.Base), token, nil, &base); err != nil || base.Ref != plan.Ref || base.Object.Type != "commit" || !gitSHA(base.Object.SHA) {
		return PullRequest{}, ErrUnavailable
	}
	// The Contents API creates without an old file SHA. The same absence is
	// checked before creating the branch to avoid leaving needless partial work.
	status, err := c.callStatus(ctx, http.MethodGet, basePath+"/contents/"+plan.Path+"?ref="+base.Object.SHA, token, nil, nil)
	if err == nil {
		return PullRequest{}, ErrWorkflowExists
	}
	if status != http.StatusNotFound {
		return PullRequest{}, ErrUnavailable
	}
	status, err = c.callStatus(ctx, http.MethodGet, basePath+"/git/ref/heads/"+url.PathEscape(plan.Branch), token, nil, nil)
	if err == nil {
		return PullRequest{}, ErrBranchExists
	}
	if status != http.StatusNotFound {
		return PullRequest{}, ErrUnavailable
	}
	result := PullRequest{Branch: plan.Branch}
	body, _ := json.Marshal(map[string]string{"ref": "refs/heads/" + plan.Branch, "sha": base.Object.SHA})
	if err := c.call(ctx, http.MethodPost, basePath+"/git/refs", token, body, nil); err != nil {
		return result, ErrUnconfirmed
	}
	body, _ = json.Marshal(map[string]string{"message": "Add Ozal publishing for " + plan.Project, "branch": plan.Branch, "content": base64.StdEncoding.EncodeToString([]byte(plan.Content))})
	if err := c.call(ctx, http.MethodPut, basePath+"/contents/"+plan.Path, token, body, nil); err != nil {
		return result, ErrUnconfirmed
	}
	body, _ = json.Marshal(map[string]any{
		"title": "Publish " + plan.Project + " through Ozal",
		"head":  plan.Branch, "base": plan.Base,
		"body": fmt.Sprintf("Publish the built static site to %s through the project-scoped Actions OIDC endpoint.\n\nReview the npm build directory (%s), output (%s), pinned tools and deployment settings before merging. Builds run on GitHub-hosted runners without OIDC; only the deployment job can request a token. Public acceptance restores the last cached accepted build on failure when one exists. The first failed publication requires an operator to restore a retained version if no cache exists.\n\nWorkflow SHA-256: `%s`. Ozal has not merged this PR, run its build, changed DNS or deployed the site.", plan.SiteURL, plan.Directory, plan.Output, plan.Digest),
	})
	var response struct {
		URL    string `json:"html_url"`
		Number int64  `json:"number"`
	}
	if err := c.call(ctx, http.MethodPost, basePath+"/pulls", token, body, &response); err != nil || response.Number < 1 || response.URL != fmt.Sprintf("https://github.com/%s/pull/%d", current.FullName, response.Number) {
		return result, ErrUnconfirmed
	}
	result.URL, result.Number = response.URL, response.Number
	return result, nil
}

func gitSHA(s string) bool {
	if len(s) != 40 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
