// Package deployapi implements the narrow, project-authenticated deploy HTTP surface.
package deployapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/switchboard-project/switchboard/internal/pages/deployauth"
)

const GitHubIssuer = "https://token.actions.githubusercontent.com"
const GitHubJWKS = GitHubIssuer + "/.well-known/jwks"

var ErrInvalidGitHubToken = errors.New("GitHub token could not be verified")

// GitHubVerifier keeps one cached key set pinned to GitHub. The SDK checks
// signature/issuer/audience/expiry; deployauth adds project trust and lifetime.
type GitHubVerifier struct {
	now      func() time.Time
	keys     *githubKeys
	verifier *oidc.IDTokenVerifier
}

// NewGitHubVerifier fetches keys lazily, on the first token. The context is
// kept for API compatibility; each fetch is bound to the verifying request.
func NewGitHubVerifier(_ context.Context, audience string, client *http.Client) *GitHubVerifier {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	v := &GitHubVerifier{now: time.Now}
	v.keys = &githubKeys{client: client, url: GitHubJWKS, now: func() time.Time { return v.now() }}
	v.verifier = oidc.NewVerifier(GitHubIssuer, v.keys, &oidc.Config{ClientID: audience, SupportedSigningAlgs: []string{"RS256"}, Now: func() time.Time { return v.now() }})
	return v
}
func (v *GitHubVerifier) Verify(ctx context.Context, token string) (deployauth.Claims, error) {
	if len(token) > 16<<10 {
		return deployauth.Claims{}, ErrInvalidGitHubToken
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	verified, err := v.verifier.Verify(ctx, token)
	if err != nil {
		return deployauth.Claims{}, ErrInvalidGitHubToken
	}
	var raw struct {
		RepositoryID string `json:"repository_id"`
		OwnerID      string `json:"repository_owner_id"`
		Ref          string `json:"ref"`
		WorkflowRef  string `json:"workflow_ref"`
		EventName    string `json:"event_name"`
		JWTID        string `json:"jti"`
		SHA          string `json:"sha"`
		Repository   string `json:"repository"`
		RunID        string `json:"run_id"`
		NotBefore    int64  `json:"nbf"`
	}
	if err := verified.Claims(&raw); err != nil {
		return deployauth.Claims{}, ErrInvalidGitHubToken
	}
	observedAt := v.now().UTC()
	if raw.NotBefore > observedAt.Add(30*time.Second).Unix() {
		return deployauth.Claims{}, ErrInvalidGitHubToken
	}
	repositoryID, err := strconv.ParseInt(raw.RepositoryID, 10, 64)
	if err != nil || repositoryID < 1 {
		return deployauth.Claims{}, ErrInvalidGitHubToken
	}
	ownerID, err := strconv.ParseInt(raw.OwnerID, 10, 64)
	if err != nil || ownerID < 1 {
		return deployauth.Claims{}, ErrInvalidGitHubToken
	}
	claims := deployauth.Claims{RepositoryID: repositoryID, RepositoryOwnerID: ownerID, Ref: raw.Ref, WorkflowRef: raw.WorkflowRef, EventName: raw.EventName,
		JWTID: raw.JWTID, CommitSHA: raw.SHA, Repository: raw.Repository, RunID: raw.RunID, Audience: verified.Audience,
		IssuedAt: verified.IssuedAt, ExpiresAt: verified.Expiry, ObservedAt: observedAt}
	if claims.WorkflowRunURL() == "" {
		return deployauth.Claims{}, ErrInvalidGitHubToken
	}
	return claims, nil
}
