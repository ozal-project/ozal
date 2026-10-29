package auth

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/policycontext"
)

// Explanation is the outcome of simulating one request against one route
// without live HTTP, OIDC, sessions, cookies, TLS or an upstream (ARCH-008).
// Context contains the supplied identity facts and explicit unknowns for
// evidence that an offline simulation cannot observe.
type Explanation struct {
	// Protected reports whether the route requires an authenticated identity.
	Protected bool
	// AtGate is true when the decision was taken by the Gate (no session)
	// before Authorize could run.
	AtGate   bool
	Decision Decision
	Context  policycontext.Context
}

// Explain returns the decision the proxy would take for route r. A nil p
// simulates a request without a session: a protected route then stops at the
// Gate with CodeAuthenticationRequired and Authorize is never called, exactly
// as in the proxy. A non-nil p is evaluated by the same Authorize the proxy
// uses, with the same route.Policy(). Network and request-specific context
// fields remain unknown because the simulation does not make an HTTP request.
func Explain(r config.Route, p *Principal) Explanation {
	contextPrincipal := p
	if !r.RequiresAuth() {
		contextPrincipal = nil
	}
	return ExplainWithContext(r, p, simulatedContext(r, contextPrincipal, time.Now().UTC(), ""))
}

// ExplainWithContext uses the supplied evidence snapshot and the same pure
// policy engine as the request path. It does not infer any missing signal.
func ExplainWithContext(r config.Route, p *Principal, context policycontext.Context) Explanation {
	if !r.RequiresAuth() {
		return Explanation{Context: context, Decision: Decision{
			Allowed: true,
			Code:    CodeAllowed,
			Reason:  "public route; no access policy",
		}}
	}
	if p == nil {
		return Explanation{Protected: true, AtGate: true, Context: context, Decision: Decision{
			Code:   CodeAuthenticationRequired,
			Reason: "authentication required before Authorize is called",
		}}
	}
	decision := AuthorizeWithContext(*p, context, r.Policy())
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
	return Explanation{Protected: true, Decision: decision, Context: context}
}

// Scope values are part of the Report contract; keep them stable.
const (
	ScopeAuthorization = "authorization_only"
	ScopeGate          = "gate_only"
	ScopePublic        = "public_route"
)

// Report is the stable, machine-readable form of an Explanation shared by
// `ozal policy explain -json` and the admin API. Field names are a
// public contract: version them if they ever change.
type Report struct {
	Route     string                `json:"route"`
	Protected bool                  `json:"protected"`
	Policy    *config.AccessPolicy  `json:"policy,omitempty"`
	Principal *ReportPrincipal      `json:"principal,omitempty"`
	Context   policycontext.Context `json:"context"`
	Decision  ReportDecision        `json:"decision"`
	Scope     string                `json:"scope"`
}

type ReportPrincipal struct {
	Email           string   `json:"email"`
	EmailVerified   bool     `json:"email_verified"`
	Groups          []string `json:"groups"`
	Method          string   `json:"method,omitempty"`
	DeviceBound     *bool    `json:"device_bound,omitempty"`
	UserVerified    *bool    `json:"user_verified,omitempty"`
	DeviceFirstSeen *bool    `json:"device_first_seen,omitempty"`
}

type ReportDecision struct {
	Allowed bool   `json:"allowed"`
	StepUp  bool   `json:"step_up"`
	Code    string `json:"code"`
	Reason  string `json:"reason"`
}

// Verdict is "allowed", "denied" or "step_up".
func (d ReportDecision) Verdict() string {
	if d.StepUp {
		return "step_up"
	}
	if d.Allowed {
		return "allowed"
	}
	return "denied"
}

// NewReport explains r for p and renders the result. host is the normalized
// route host. The principal is echoed only for protected routes, where it was
// actually evaluated.
func NewReport(host string, r config.Route, p *Principal) Report {
	return NewReportWithRequestMethod(host, r, p, "")
}

// NewReportWithRequestMethod builds the CLI/admin report with an optional
// hypothetical HTTP method. The method is explicitly marked as simulation
// input and only classifies risk when the route owner declared risky_actions.
func NewReportWithRequestMethod(host string, r config.Route, p *Principal, requestMethod string) Report {
	contextPrincipal := p
	if !r.RequiresAuth() {
		contextPrincipal = nil
	}
	ex := ExplainWithContext(r, p, simulatedContext(r, contextPrincipal, time.Now().UTC(), requestMethod))
	rep := Report{
		Route:     host,
		Protected: ex.Protected,
		Context:   ex.Context,
		Decision:  ReportDecision{Allowed: ex.Decision.Allowed, StepUp: ex.Decision.StepUp, Code: string(ex.Decision.Code), Reason: ex.Decision.Reason},
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
		rep.Principal = &ReportPrincipal{Email: p.Email, EmailVerified: p.EmailVerified, Groups: groups, Method: p.Method}
		if p.Method != "" {
			deviceBound := p.DeviceBound
			rep.Principal.DeviceBound = &deviceBound
		}
		if p.Method == MethodPasskey {
			rep.Principal.UserVerified = p.UserVerified
			rep.Principal.DeviceFirstSeen = p.DeviceFirstSeen
		}
	}
	return rep
}

func simulatedContext(route config.Route, p *Principal, observedAt time.Time, requestMethod string) policycontext.Context {
	input := policycontext.Input{
		ObservedAt:      observedAt,
		ArrivalPath:     policycontext.Unknown[string](policycontext.SourceSimulationNoRequest),
		VisitorIPSource: policycontext.Unknown[string](policycontext.SourceSimulationNoRequest),
		Owner:           policycontext.Unknown[string](policycontext.SourceUnconfigured),
		DataClass:       policycontext.Unknown[string](policycontext.SourceUnconfigured),
		Sensitivity:     policycontext.Unknown[string](policycontext.SourceUnconfigured),
		HTTPMethod:      policycontext.Unknown[string](policycontext.SourceSimulationNoRequest),
		PathClass:       policycontext.Unknown[string](policycontext.SourceSimulationNoRequest),
		Risky:           policycontext.Unknown[bool](policycontext.SourceSimulationNoRequest),
		DeviceFirstSeen: policycontext.Unknown[bool](policycontext.SourceNotCollected),
	}
	if route.Resource != nil {
		input.Owner = policycontext.Known(route.Resource.Owner, policycontext.SourceRouteConfig)
		input.DataClass = policycontext.Known(route.Resource.DataClass, policycontext.SourceRouteConfig)
		input.Sensitivity = policycontext.Known(route.Resource.Sensitivity, policycontext.SourceRouteConfig)
		if route.Resource.RiskyActions == nil {
			input.Risky = policycontext.Unknown[bool](policycontext.SourceUnconfigured)
		}
	}
	if requestMethod != "" {
		input.HTTPMethod = policycontext.Known(requestMethod, policycontext.SourceSimulationInput)
		if route.Resource != nil && route.Resource.RiskyActions != nil {
			input.Risky = policycontext.Known(slices.Contains(route.Resource.RiskyActions, requestMethod), policycontext.SourceRouteConfig)
		}
	}
	if route.Host != "" {
		input.Route = policycontext.Known(route.Host, policycontext.SourceRouteTable)
	} else {
		input.Route = policycontext.Unknown[string](policycontext.SourceNotRecorded)
	}
	if p == nil {
		input.SessionMethod = policycontext.Unknown[string](policycontext.SourceNoSession)
		input.SessionAuthTime = policycontext.Unknown[time.Time](policycontext.SourceNoSession)
		input.SessionUserVerified = policycontext.Unknown[bool](policycontext.SourceNoSession)
		input.SessionDeviceBound = policycontext.Unknown[bool](policycontext.SourceNoSession)
		input.DeviceFirstSeen = policycontext.Unknown[bool](policycontext.SourceNoSession)
	} else {
		if p.Method != "" {
			input.SessionMethod = policycontext.Known(p.Method, policycontext.SourceSession)
			input.SessionDeviceBound = policycontext.Known(p.DeviceBound, policycontext.SourceSession)
		} else {
			input.SessionMethod = policycontext.Unknown[string](policycontext.SourceNotRecorded)
			input.SessionDeviceBound = policycontext.Unknown[bool](policycontext.SourceNotRecorded)
		}
		if !p.AuthTime.IsZero() {
			input.SessionAuthTime = policycontext.Known(p.AuthTime, policycontext.SourceSession)
		} else {
			input.SessionAuthTime = policycontext.Unknown[time.Time](policycontext.SourceNotRecorded)
		}
		if p.Method == MethodPasskey && p.UserVerified != nil {
			input.SessionUserVerified = policycontext.Known(*p.UserVerified, policycontext.SourceSimulationInput)
		} else {
			input.SessionUserVerified = policycontext.Unknown[bool](policycontext.SourceNotRecorded)
		}
		if p.Method == MethodPasskey && p.DeviceFirstSeen != nil {
			input.DeviceFirstSeen = policycontext.Known(*p.DeviceFirstSeen, policycontext.SourceSimulationInput)
		} else {
			input.DeviceFirstSeen = policycontext.Unknown[bool](policycontext.SourceNotRecorded)
		}
	}
	return policycontext.Build(input)
}
