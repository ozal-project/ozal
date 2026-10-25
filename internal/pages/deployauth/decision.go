// Package deployauth contains the pure GitHub Actions deployment decision.
package deployauth

import (
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const MaxTokenLifetime = 10 * time.Minute

// deployEvents are the GitHub events that may publish: a push to the trusted
// ref, or a manual run by someone with write access. Pull request events,
// including pull_request_target, never deploy.
var deployEvents = []string{"push", "workflow_dispatch"}

// Claims are verified token claims plus ObservedAt, supplied by the verifier's
// local clock. ObservedAt is not read from the JWT.
type Claims struct {
	RepositoryID, RepositoryOwnerID                                  int64
	Ref, WorkflowRef, EventName, JWTID, CommitSHA, Repository, RunID string
	Audience                                                         []string
	IssuedAt, ExpiresAt, ObservedAt                                  time.Time
}

type Trust struct {
	RepositoryID, RepositoryOwnerID int64
	Ref, WorkflowRef, Audience      string
}

type Decision struct {
	Allowed      bool
	Code, Reason string
}

func DecideDeployer(claims Claims, trust Trust) Decision {
	deny := func(code, reason string) Decision { return Decision{Code: code, Reason: reason} }
	if trust.Audience == "" || !slices.Contains(claims.Audience, trust.Audience) {
		return deny("deployer.audience", "token audience does not match this deploy endpoint")
	}
	if claims.ObservedAt.IsZero() || !claims.ExpiresAt.After(claims.ObservedAt) {
		return deny("deployer.expired", "token has expired or lacks expiry evidence")
	}
	if claims.IssuedAt.IsZero() || claims.IssuedAt.After(claims.ObservedAt.Add(30*time.Second)) ||
		!claims.ExpiresAt.After(claims.IssuedAt) || claims.ExpiresAt.Sub(claims.IssuedAt) > MaxTokenLifetime {
		return deny("deployer.lifetime", "token lifetime is not within the short deployment window")
	}
	if trust.RepositoryID < 1 || claims.RepositoryID != trust.RepositoryID {
		return deny("deployer.repository", "repository is not trusted for this project")
	}
	if trust.RepositoryOwnerID < 1 || claims.RepositoryOwnerID != trust.RepositoryOwnerID {
		return deny("deployer.owner", "repository owner is not trusted for this project")
	}
	if trust.Ref == "" || claims.Ref != trust.Ref {
		return deny("deployer.ref", "ref is not trusted for this project")
	}
	// The ref alone is not enough: a pull_request_target workflow runs with the
	// base branch's ref while handling a fork's pull request. Pinning the
	// workflow file and the triggering event keeps that token out.
	if trust.WorkflowRef == "" || claims.WorkflowRef != trust.WorkflowRef {
		return deny("deployer.workflow", "workflow is not trusted for this project")
	}
	if !slices.Contains(deployEvents, claims.EventName) {
		return deny("deployer.event", "workflow event cannot deploy")
	}
	if claims.JWTID == "" || len(claims.JWTID) > 256 || strings.IndexFunc(claims.JWTID, unicode.IsControl) >= 0 || !ValidCommit(claims.CommitSHA) {
		return deny("deployer.claims", "token lacks valid replay or commit evidence")
	}
	return Decision{Allowed: true, Code: "deployer.allowed", Reason: "configured deployment identity matched"}
}

// ValidCommit bounds source provenance to lowercase Git SHA-1 or SHA-256 hex.
func ValidCommit(value string) bool {
	if (len(value) != 40 && len(value) != 64) || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// WorkflowRunURL derives provenance only from bounded, verified claim values.
func (c Claims) WorkflowRunURL() string {
	parts := strings.Split(c.Repository, "/")
	if len(parts) != 2 || len(c.Repository) > 201 || len(c.RunID) > 20 {
		return ""
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return ""
		}
		for _, ch := range part {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.') {
				return ""
			}
		}
	}
	run, err := strconv.ParseUint(c.RunID, 10, 64)
	if err != nil || run == 0 {
		return ""
	}
	return "https://github.com/" + c.Repository + "/actions/runs/" + c.RunID
}
