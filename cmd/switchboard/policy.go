package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/policycontext"
)

var scopeText = map[string]string{
	auth.ScopeAuthorization: "authorization only; supplied session evidence is hypothetical, and OIDC, TLS and transport are not evaluated",
	auth.ScopeGate:          "gate decision only; no OIDC flow was started",
	auth.ScopePublic:        "public route; no identity is evaluated",
}

// runPolicy dispatches `switchboard policy <subcommand>`.
func runPolicy(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard policy <explain|replay|who-can|review> [flags]")
	}
	switch args[0] {
	case "explain":
		return runPolicyExplain(ctx, args[1:], out)
	case "replay":
		return runPolicyReplay(ctx, args[1:], out)
	case "who-can":
		return runPolicyWhoCan(ctx, args[1:], out)
	case "review":
		return runPolicyReview(ctx, args[1:], out)
	default:
		return errors.New("usage: switchboard policy <explain|replay|who-can|review> [flags]")
	}
}

// runPolicyExplain explains why a route would allow or deny an example
// Principal, using the same auth.Authorize and route policy as the proxy. It
// binds no listener, starts no OIDC flow and never prints the auth section of
// the config (secrets or their references).
func runPolicyExplain(_ context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("policy explain", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: switchboard policy explain -host HOST [flags]")
		fmt.Fprintln(fs.Output(), "Explains an access decision. Supply session and request-method evidence to evaluate context rules; live login, TLS and transport are not evaluated.")
		fs.PrintDefaults()
	}
	path := fs.String("config", "config.json", "path to routing config")
	host := fs.String("host", "", "route host to evaluate (required)")
	email := fs.String("email", "", "example principal email (required for a protected route unless -no-session)")
	verified := fs.Bool("verified", false, "the provider verified the email")
	groups := fs.String("groups", "", "comma-separated groups of the principal")
	method := fs.String("method", "", "simulated session method: oidc or passkey (used by context rules)")
	deviceBound := fs.Bool("device-bound", false, "simulated passkey cannot be synced (requires -method passkey)")
	userVerified := fs.String("user-verified", "", "simulated authenticator user verification: true or false (requires -method passkey; empty means unknown)")
	deviceFirstSeen := fs.String("device-first-seen", "", "simulated newly added passkey evidence: true or false (requires -method passkey; empty means unknown)")
	requestMethod := fs.String("request-method", "", "hypothetical HTTP method used by action_risky rules")
	noSession := fs.Bool("no-session", false, "simulate a request without a session (Gate level)")
	asJSON := fs.Bool("json", false, "print stable JSON")
	expect := fs.String("expect", "", "fail unless the decision is allowed, denied or step_up")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *host == "" {
		return errors.New("-host is required")
	}
	if *expect != "" && *expect != "allowed" && *expect != "denied" && *expect != "step_up" {
		return fmt.Errorf("-expect must be allowed, denied or step_up, got %q", *expect)
	}
	if *noSession && (*email != "" || *verified || *groups != "" || *method != "" || *deviceBound || *userVerified != "" || *deviceFirstSeen != "" || *requestMethod != "") {
		return errors.New("-no-session cannot be combined with principal, session, or request evidence flags")
	}
	if *userVerified != "" && *userVerified != "true" && *userVerified != "false" {
		return errors.New("-user-verified must be true or false")
	}
	if *deviceFirstSeen != "" && *deviceFirstSeen != "true" && *deviceFirstSeen != "false" {
		return errors.New("-device-first-seen must be true or false")
	}
	if *requestMethod != "" && !config.IsSupportedHTTPMethod(*requestMethod) {
		return fmt.Errorf("-request-method %q is not a supported HTTP method", *requestMethod)
	}

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if *method != "" && !slices.Contains(cfg.Auth.LoginMethods(), *method) {
		return fmt.Errorf("-method %q is not configured (have %v)", *method, cfg.Auth.LoginMethods())
	}
	if *deviceBound && *method != auth.MethodPasskey {
		return errors.New("-device-bound requires -method passkey")
	}
	if *userVerified != "" && *method != auth.MethodPasskey {
		return errors.New("-user-verified requires -method passkey")
	}
	if *deviceFirstSeen != "" && *method != auth.MethodPasskey {
		return errors.New("-device-first-seen requires -method passkey")
	}
	normalized, err := config.NormalizeHost(*host)
	if err != nil {
		return err
	}
	route, ok := findRoute(cfg.Routes, normalized)
	if !ok {
		return fmt.Errorf("no route for host %q", normalized)
	}

	var principal *auth.Principal
	var verifiedByAuthenticator *bool
	var firstSeenDevice *bool
	if *userVerified != "" {
		value := *userVerified == "true"
		verifiedByAuthenticator = &value
	}
	if *deviceFirstSeen != "" {
		value := *deviceFirstSeen == "true"
		firstSeenDevice = &value
	}
	if *email != "" {
		principal = &auth.Principal{Email: *email, EmailVerified: *verified, Groups: splitGroups(*groups), Method: *method, DeviceBound: *deviceBound, UserVerified: verifiedByAuthenticator, DeviceFirstSeen: firstSeenDevice}
	} else if route.RequiresAuth() && !*noSession {
		return fmt.Errorf("route %q is protected: pass -email (with -verified/-groups and optional session flags) or -no-session", normalized)
	} else if *verified || *groups != "" {
		return errors.New("-verified and -groups need -email")
	} else if *method != "" || *deviceBound || *userVerified != "" || *deviceFirstSeen != "" {
		return errors.New("-method, -device-bound, -user-verified and -device-first-seen need -email")
	}

	report := auth.NewReportWithRequestMethod(normalized, route, principal, *requestMethod)
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		writeExplainText(out, report)
	}

	if *expect != "" && *expect != report.Decision.Verdict() {
		return fmt.Errorf("decision is %s, expected %s", report.Decision.Verdict(), *expect)
	}
	return nil
}

func findRoute(routes []config.Route, host string) (config.Route, bool) {
	for _, r := range routes {
		if r.Host == host { // Load already normalized route hosts
			return r, true
		}
	}
	return config.Route{}, false
}

func splitGroups(raw string) []string {
	groups := []string{}
	for g := range strings.SplitSeq(raw, ",") {
		if g = strings.TrimSpace(g); g != "" {
			groups = append(groups, g)
		}
	}
	return groups
}

func writeExplainText(out io.Writer, r auth.Report) {
	kind := "public"
	if r.Protected {
		kind = "protected"
	}
	fmt.Fprintf(out, "route: %s (%s)\n", r.Route, kind)
	if r.Policy != nil {
		fmt.Fprintf(out, "policy: %s\n", describePolicy(*r.Policy))
	}
	if r.Principal != nil {
		method, deviceBound := r.Principal.Method, "unknown"
		if method == "" {
			method = "unknown"
		}
		if r.Principal.DeviceBound != nil {
			deviceBound = fmt.Sprint(*r.Principal.DeviceBound)
		}
		firstSeen := "unknown"
		if r.Principal.DeviceFirstSeen != nil {
			firstSeen = fmt.Sprint(*r.Principal.DeviceFirstSeen)
		}
		fmt.Fprintf(out, "principal: %s verified=%t groups=[%s] method=%s device_bound=%s device_first_seen=%s\n",
			r.Principal.Email, r.Principal.EmailVerified, strings.Join(r.Principal.Groups, ","), method, deviceBound, firstSeen)
	} else if r.Scope == auth.ScopeGate {
		fmt.Fprintln(out, "principal: none (no session)")
	}
	fmt.Fprintf(out, "decision: %s\ncode: %s\nreason: %s\nscope: %s\n",
		r.Decision.Verdict(), r.Decision.Code, r.Decision.Reason, scopeText[r.Scope])
	fmt.Fprintf(out, "context (evidence only; unknown is not low; observed_at=%s):\n",
		r.Context.Action.HTTPMethod.ObservedAt.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(out, "  session: method=%s auth_time=%s user_verified=%s device_bound=%s\n",
		signalSummary(r.Context.Session.Method), signalSummary(r.Context.Session.AuthTime),
		signalSummary(r.Context.Session.UserVerified), signalSummary(r.Context.Session.DeviceBound))
	fmt.Fprintf(out, "  network: arrival_path=%s visitor_ip_source=%s\n",
		signalSummary(r.Context.Network.ArrivalPath), signalSummary(r.Context.Network.VisitorIPSource))
	fmt.Fprintf(out, "  resource: route=%s owner=%s data_class=%s sensitivity=%s\n",
		signalSummary(r.Context.Resource.Route), signalSummary(r.Context.Resource.Owner),
		signalSummary(r.Context.Resource.DataClass), signalSummary(r.Context.Resource.Sensitivity))
	fmt.Fprintf(out, "  action: method=%s path_class=%s risky=%s history.device_first_seen=%s\n",
		signalSummary(r.Context.Action.HTTPMethod), signalSummary(r.Context.Action.PathClass),
		signalSummary(r.Context.Action.Risky),
		signalSummary(r.Context.History.DeviceFirstSeen))
}

func signalSummary[T any](signal policycontext.Signal[T]) string {
	if signal.Value == nil {
		return "unknown (source=" + signal.Source + ")"
	}
	return fmt.Sprintf("%v (source=%s)", *signal.Value, signal.Source)
}

func describePolicy(p config.AccessPolicy) string {
	var parts []string
	if p.ContextRule != nil {
		parts = append(parts, "context_rule=enabled then="+p.ContextRule.Then)
	}
	if len(p.AllowedEmails) > 0 {
		parts = append(parts, "allowed_emails=["+strings.Join(p.AllowedEmails, ",")+"]")
	}
	if len(p.AllowedDomains) > 0 {
		parts = append(parts, "allowed_domains=["+strings.Join(p.AllowedDomains, ",")+"]")
	}
	if len(p.RequiredGroups) > 0 {
		parts = append(parts, "required_groups=["+strings.Join(p.RequiredGroups, ",")+"]")
	}
	if len(parts) == 0 {
		return "any authenticated user"
	}
	return strings.Join(parts, " ")
}
