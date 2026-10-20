package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
)

var scopeText = map[string]string{
	auth.ScopeAuthorization: "authorization only; OIDC, session and transport are not evaluated",
	auth.ScopeGate:          "gate decision only; no OIDC flow was started",
	auth.ScopePublic:        "public route; no identity is evaluated",
}

// runPolicy dispatches `switchboard policy <subcommand>`.
func runPolicy(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "explain" {
		return errors.New("usage: switchboard policy explain -config FILE -host HOST [-email E -verified -groups G1,G2 | -no-session] [-json] [-expect allowed|denied]")
	}
	return runPolicyExplain(ctx, args[1:], out)
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
		fmt.Fprintln(fs.Output(), "Explains an access decision. Scope: authorization only; OIDC, session, TLS and transport are not evaluated.")
		fs.PrintDefaults()
	}
	path := fs.String("config", "config.json", "path to routing config")
	host := fs.String("host", "", "route host to evaluate (required)")
	email := fs.String("email", "", "example principal email (required for a protected route unless -no-session)")
	verified := fs.Bool("verified", false, "the provider verified the email")
	groups := fs.String("groups", "", "comma-separated groups of the principal")
	noSession := fs.Bool("no-session", false, "simulate a request without a session (Gate level)")
	asJSON := fs.Bool("json", false, "print stable JSON")
	expect := fs.String("expect", "", "fail unless the decision is allowed or denied")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *host == "" {
		return errors.New("-host is required")
	}
	if *expect != "" && *expect != "allowed" && *expect != "denied" {
		return fmt.Errorf("-expect must be allowed or denied, got %q", *expect)
	}
	if *noSession && (*email != "" || *verified || *groups != "") {
		return errors.New("-no-session cannot be combined with -email, -verified or -groups")
	}

	cfg, err := config.Load(*path)
	if err != nil {
		return err
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
	if *email != "" {
		principal = &auth.Principal{Email: *email, EmailVerified: *verified, Groups: splitGroups(*groups)}
	} else if route.RequiresAuth() && !*noSession {
		return fmt.Errorf("route %q is protected: pass -email (with -verified/-groups) or -no-session", normalized)
	} else if *verified || *groups != "" {
		return errors.New("-verified and -groups need -email")
	}

	report := auth.NewReport(normalized, route, principal)
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
		fmt.Fprintf(out, "principal: %s verified=%t groups=[%s]\n",
			r.Principal.Email, r.Principal.EmailVerified, strings.Join(r.Principal.Groups, ","))
	} else if r.Scope == auth.ScopeGate {
		fmt.Fprintln(out, "principal: none (no session)")
	}
	fmt.Fprintf(out, "decision: %s\ncode: %s\nreason: %s\nscope: %s\n",
		r.Decision.Verdict(), r.Decision.Code, r.Decision.Reason, scopeText[r.Scope])
}

func describePolicy(p config.AccessPolicy) string {
	var parts []string
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
