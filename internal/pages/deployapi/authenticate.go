package deployapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"strconv"
	"strings"
	"unicode"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/pages/deployauth"
)

type identity struct {
	claims *deployauth.Claims
	source events.DeploymentSource
	actor  string
}

func safeSource(project string, c deployauth.Claims) events.DeploymentSource {
	source := events.DeploymentSource{Project: project}
	if c.RepositoryID > 0 {
		source.RepositoryID = c.RepositoryID
	}
	if len(c.Ref) <= 256 && strings.IndexFunc(c.Ref, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) < 0 {
		source.Ref = c.Ref
	}
	if deployauth.ValidCommit(c.CommitSHA) {
		source.CommitSHA = c.CommitSHA
	}
	return source
}

func (h *Handler) authenticate(ctx context.Context, project config.Project, token string) (identity, string, int) {
	id := identity{source: events.DeploymentSource{Project: project.Name}}
	provided := sha256.Sum256([]byte(token))
	matched := 0
	unavailable := false
	hasGitHub := false
	for _, deployer := range project.Deployers {
		if deployer.GitHub != nil {
			hasGitHub = true
		}
		if deployer.Token == "" {
			continue
		}
		if !strings.HasPrefix(deployer.Token, "env:") && !strings.HasPrefix(deployer.Token, "file:") {
			unavailable = true
			continue
		}
		secret, err := h.options.ResolveSecret(deployer.Token)
		if err != nil || secret == "" {
			unavailable = true
			continue
		}
		candidate := sha256.Sum256([]byte(secret))
		matched |= subtle.ConstantTimeCompare(provided[:], candidate[:])
	}
	if matched == 1 {
		id.actor = "project-token"
		return id, "", 0
	}
	if !hasGitHub {
		if unavailable {
			return id, "deployer.unavailable", 503
		}
		return id, "deployer.token_invalid", 401
	}
	if h.options.Verifier == nil {
		return id, "deployer.unavailable", 503
	}
	claims, err := h.options.Verifier.Verify(ctx, token)
	if err != nil {
		return id, "deployer.token_invalid", 401
	}
	id.source = safeSource(project.Name, claims)
	code := "deployer.repository"
	for _, deployer := range project.Deployers {
		if deployer.GitHub == nil {
			continue
		}
		github := deployer.GitHub
		decision := deployauth.DecideDeployer(claims, deployauth.Trust{RepositoryID: github.RepositoryID, RepositoryOwnerID: github.RepositoryOwnerID, Ref: github.Ref, WorkflowRef: github.WorkflowRef, Audience: h.options.Audience})
		if decision.Allowed {
			id.claims = &claims
			id.actor = "github:" + strconv.FormatInt(claims.RepositoryID, 10)
			return id, "", 0
		}
		if github.RepositoryID == claims.RepositoryID || code == "deployer.repository" {
			code = decision.Code
		}
	}
	return id, code, 401
}
