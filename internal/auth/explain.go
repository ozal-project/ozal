package auth

import (
	"fmt"
	"strings"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

// Explanation is the outcome of simulating one request against one route
// without HTTP, OIDC or sessions (ARCH-008). It only covers the decision: it
// says nothing about login, cookies, TLS or the upstream.
type Explanation struct {
	// Protected reports whether the route requires an authenticated identity.
	Protected bool
	// AtGate is true when the decision was taken by the Gate (no session)
	// before Authorize could run.
	AtGate   bool
	Decision Decision
}

// Explain returns the decision the proxy would take for route r. A nil p
// simulates a request without a session: a protected route then stops at the
// Gate with CodeAuthenticationRequired and Authorize is never called, exactly
// as in the proxy. A non-nil p is evaluated by the same Authorize the proxy
// uses, with the same route.Policy().
func Explain(r config.Route, p *Principal) Explanation {
	if !r.RequiresAuth() {
		return Explanation{Decision: Decision{
			Allowed: true,
			Code:    CodeAllowed,
			Reason:  "public route; no access policy",
		}}
	}
	if p == nil {
		return Explanation{Protected: true, AtGate: true, Decision: Decision{
			Code:   CodeAuthenticationRequired,
			Reason: "authentication required before Authorize is called",
		}}
	}
	decision := Authorize(*p, r.Policy())
	// The simulation has no clock or session, so the login requirements are
	// stated rather than evaluated: an allowed visitor may still be asked to
	// log in again.
	if policy := r.Policy(); decision.Allowed {
		var needs []string
		if len(policy.Methods) > 0 {
			needs = append(needs, "a login with "+strings.Join(policy.Methods, " or ")+" (methods)")
		}
		if policy.RequireDeviceBound {
			needs = append(needs, "a passkey that cannot be synced (require_device_bound)")
		}
		if policy.MaxAuthAgeMinutes > 0 {
			needs = append(needs, fmt.Sprintf("a login within the last %d min (max_auth_age_minutes)", policy.MaxAuthAgeMinutes))
		}
		if len(needs) > 0 {
			decision.Reason = "allowed; the session must also have " + strings.Join(needs, ", ")
		}
	}
	return Explanation{Protected: true, Decision: decision}
}

// Scope values are part of the Report contract; keep them stable.
const (
	ScopeAuthorization = "authorization_only"
	ScopeGate          = "gate_only"
	ScopePublic        = "public_route"
)

// Report is the stable, machine-readable form of an Explanation shared by
// `switchboard policy explain -json` and the admin API. Field names are a
// public contract: version them if they ever change.
type Report struct {
	Route     string               `json:"route"`
	Protected bool                 `json:"protected"`
	Policy    *config.AccessPolicy `json:"policy,omitempty"`
	Principal *ReportPrincipal     `json:"principal,omitempty"`
	Decision  ReportDecision       `json:"decision"`
	Scope     string               `json:"scope"`
}

type ReportPrincipal struct {
	Email         string   `json:"email"`
	EmailVerified bool     `json:"email_verified"`
	Groups        []string `json:"groups"`
}

type ReportDecision struct {
	Allowed bool   `json:"allowed"`
	Code    string `json:"code"`
	Reason  string `json:"reason"`
}

// Verdict is "allowed" or "denied".
func (d ReportDecision) Verdict() string {
	if d.Allowed {
		return "allowed"
	}
	return "denied"
}

// NewReport explains r for p and renders the result. host is the normalized
// route host. The principal is echoed only for protected routes, where it was
// actually evaluated.
func NewReport(host string, r config.Route, p *Principal) Report {
	ex := Explain(r, p)
	rep := Report{
		Route:     host,
		Protected: ex.Protected,
		Decision:  ReportDecision{Allowed: ex.Decision.Allowed, Code: string(ex.Decision.Code), Reason: ex.Decision.Reason},
		Scope:     ScopeAuthorization,
	}
	switch {
	case !ex.Protected:
		rep.Scope = ScopePublic
	case ex.AtGate:
		rep.Scope = ScopeGate
	}
	if ex.Protected {
		policy := r.Policy()
		rep.Policy = &policy
	}
	if p != nil && ex.Protected {
		groups := p.Groups
		if groups == nil {
			groups = []string{} // always a JSON array, never null
		}
		rep.Principal = &ReportPrincipal{Email: p.Email, EmailVerified: p.EmailVerified, Groups: groups}
	}
	return rep
}
