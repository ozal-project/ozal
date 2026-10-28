package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/switchboard-project/switchboard/internal/audit"
	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
)

const maxPolicyReviewSubjects = 10_000

// policyWhoCanReport describes only the route's configured identity policy.
// It does not attempt to enumerate live provider memberships or transport
// controls, which are not available from a routing config.
type policyWhoCanReport struct {
	Host               string               `json:"host"`
	Scope              string               `json:"scope"`
	RequiresAuth       bool                 `json:"requires_auth"`
	ReachableBy        string               `json:"reachable_by"`
	LegacyAuthRequired bool                 `json:"legacy_auth_required,omitempty"`
	Policy             *config.AccessPolicy `json:"access_policy,omitempty"`
	Semantics          []string             `json:"semantics"`
}

func runPolicyWhoCan(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("policy who-can", flag.ContinueOnError)
	fs.SetOutput(out)
	configPath := fs.String("config", "config.json", "path to routing config")
	host := fs.String("host", "", "route host to inspect (required)")
	asJSON := fs.Bool("json", false, "print stable JSON")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: switchboard policy who-can -host HOST [-config PATH] [-json]")
		fmt.Fprintln(fs.Output(), "Shows configured route identity policy only; provider membership, TLS client certificates, and upstream health are not evaluated.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *host == "" {
		return errors.New("-host is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
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
	report := describeWhoCan(normalized, route)
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	writePolicyWhoCanText(out, report)
	return nil
}

func describeWhoCan(host string, route config.Route) policyWhoCanReport {
	report := policyWhoCanReport{
		Host:         host,
		Scope:        "configured route identity policy only; live provider membership, TLS/client-certificate controls, and upstream health are not evaluated",
		RequiresAuth: route.RequiresAuth(),
		Semantics:    make([]string, 0, 6),
	}
	if !route.RequiresAuth() {
		report.ReachableBy = "any visitor who passes the transport layer"
		report.Semantics = append(report.Semantics,
			"the route has no Switchboard login requirement or access identity policy",
			"TLS and client-certificate requirements may still limit transport access and are outside this report")
		return report
	}
	report.Policy = func() *config.AccessPolicy {
		policy := route.Policy()
		return &policy
	}()
	report.LegacyAuthRequired = route.AuthRequired && route.Access == nil
	policy := route.Policy()
	if report.LegacyAuthRequired {
		report.Semantics = append(report.Semantics, "legacy auth_required is equivalent to an empty access policy")
	}
	if len(policy.AllowedEmails) == 0 && len(policy.AllowedDomains) == 0 {
		report.ReachableBy = "any authenticated principal"
		if len(policy.RequiredGroups) > 0 {
			report.ReachableBy = "any authenticated principal carrying every required group"
		}
	} else {
		report.ReachableBy = "an authenticated principal whose verified email matches an allowed email or domain"
		if len(policy.RequiredGroups) > 0 {
			report.ReachableBy += " and who carries every required group"
		}
	}
	report.Semantics = append(report.Semantics, "a valid authenticated session is required")
	if len(policy.AllowedEmails) > 0 || len(policy.AllowedDomains) > 0 {
		report.Semantics = append(report.Semantics, "email allowlist matching is OR'd across allowed_emails and allowed_domains, and requires provider-verified email")
	} else {
		report.Semantics = append(report.Semantics, "no allowed_emails or allowed_domains are configured, so the email identity is not restricted")
	}
	if len(policy.RequiredGroups) > 0 {
		report.Semantics = append(report.Semantics, "every required_groups entry must be present in the authenticated principal")
	}
	if len(policy.Methods) > 0 {
		report.Semantics = append(report.Semantics, "the session must use one of the configured login methods")
	}
	if policy.MaxAuthAgeMinutes > 0 || policy.RequireDeviceBound {
		report.Semantics = append(report.Semantics, "authentication freshness and device-bound requirements may trigger a step-up")
	}
	if policy.ContextRule != nil {
		report.Semantics = append(report.Semantics, "the context_rule adds a per-request condition; run policy explain or policy replay to evaluate evidence")
	}
	return report
}

func writePolicyWhoCanText(out io.Writer, report policyWhoCanReport) {
	fmt.Fprintf(out, "route: %s\nrequires_auth: %t\nreachable_by: %s\n", report.Host, report.RequiresAuth, report.ReachableBy)
	if report.LegacyAuthRequired {
		fmt.Fprintln(out, "policy_form: legacy auth_required (equivalent to an empty access policy)")
	}
	if report.Policy != nil {
		if len(report.Policy.AllowedEmails) > 0 {
			fmt.Fprintf(out, "allowed_emails: [%s]\n", strings.Join(report.Policy.AllowedEmails, ", "))
		}
		if len(report.Policy.AllowedDomains) > 0 {
			fmt.Fprintf(out, "allowed_domains: [%s]\n", strings.Join(report.Policy.AllowedDomains, ", "))
		}
		if len(report.Policy.RequiredGroups) > 0 {
			fmt.Fprintf(out, "required_groups: [%s]\n", strings.Join(report.Policy.RequiredGroups, ", "))
		}
		if len(report.Policy.Methods) > 0 {
			fmt.Fprintf(out, "methods: [%s]\n", strings.Join(report.Policy.Methods, ", "))
		}
		if report.Policy.MaxAuthAgeMinutes > 0 {
			fmt.Fprintf(out, "max_auth_age_minutes: %d\n", report.Policy.MaxAuthAgeMinutes)
		}
		if report.Policy.RequireDeviceBound {
			fmt.Fprintln(out, "require_device_bound: true")
		}
		if report.Policy.ContextRule != nil {
			fmt.Fprintf(out, "context_rule: then=%s when=%+v\n", report.Policy.ContextRule.Then, report.Policy.ContextRule.When)
		}
	}
	for _, semantic := range report.Semantics {
		fmt.Fprintf(out, "- %s\n", semantic)
	}
	fmt.Fprintf(out, "scope: %s\n", report.Scope)
}

type policyReviewCandidate struct {
	Kind           string `json:"kind"`
	Value          string `json:"value"`
	MatchSemantics string `json:"match_semantics"`
	Usage          string `json:"usage"`
	Reason         string `json:"reason"`
}

type policyReviewSubject struct {
	Fingerprint string `json:"fingerprint"`
	Allowed     uint64 `json:"allowed_decisions"`
	Denied      uint64 `json:"denied_decisions"`
	StepUp      uint64 `json:"step_up_decisions"`
}

type policyReviewReport struct {
	Host              string                  `json:"host"`
	Scope             string                  `json:"scope"`
	GeneratedAt       time.Time               `json:"generated_at"`
	Since             time.Time               `json:"since"`
	Until             time.Time               `json:"until"`
	Audit             audit.Verification      `json:"audit_verification"`
	Candidates        []policyReviewCandidate `json:"configured_candidates"`
	SubjectLimit      int                     `json:"subject_limit"`
	DecisionEvents    uint64                  `json:"decision_events"`
	Allowed           uint64                  `json:"allowed_decisions"`
	Denied            uint64                  `json:"denied_decisions"`
	StepUp            uint64                  `json:"step_up_decisions"`
	MissingActor      uint64                  `json:"events_missing_actor"`
	ObservedSubjects  []policyReviewSubject   `json:"observed_subjects"`
	Unattributed      uint64                  `json:"unattributed_decision_events"`
	HistoryAssessment string                  `json:"candidate_history_assessment"`
}

type policyReviewSubjectCounts struct {
	allowed uint64
	denied  uint64
	stepUp  uint64
}

// runPolicyReview is intentionally conservative. Audit actors are opaque
// subjects, while route policy names email addresses and groups. This report
// keeps observed use separate from candidate eligibility rather than making
// an unsupported identity join or calling a candidate unused.
func runPolicyReview(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("policy review", flag.ContinueOnError)
	fs.SetOutput(out)
	configPath := fs.String("config", "config.json", "path to routing config")
	dir := fs.String("dir", "", "audit directory (absolute path; overrides audit.dir)")
	host := fs.String("host", "", "route host to review (required)")
	sinceText := fs.String("since", "", "start of the decision-history window (RFC3339, required)")
	untilText := fs.String("until", "", "end of the decision-history window (RFC3339; defaults to now)")
	subjectLimit := fs.Int("subjects", 100, "maximum observed subject fingerprints to retain and print (0 uses the 10000-subject safety cap)")
	asJSON := fs.Bool("json", false, "print stable JSON")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: switchboard policy review -config PATH -host HOST -since RFC3339 [-until RFC3339] [-dir ABS_PATH] [-subjects N] [-json]")
		fmt.Fprintln(fs.Output(), "Read-only access review over verified local decision history. Requires the audit writer to be stopped; unknown candidate history is never called unused.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *host == "" {
		return errors.New("-host is required")
	}
	if *sinceText == "" {
		return errors.New("-since is required to define the review window")
	}
	if *subjectLimit < 0 || *subjectLimit > maxPolicyReviewSubjects {
		return fmt.Errorf("-subjects must be between 0 and %d", maxPolicyReviewSubjects)
	}
	since, err := time.Parse(time.RFC3339Nano, *sinceText)
	if err != nil {
		return fmt.Errorf("-since must be RFC3339: %w", err)
	}
	now := time.Now().UTC()
	until := now
	if *untilText != "" {
		until, err = time.Parse(time.RFC3339Nano, *untilText)
		if err != nil {
			return fmt.Errorf("-until must be RFC3339: %w", err)
		}
		until = until.UTC()
	}
	since = since.UTC()
	if !since.Before(until) {
		return errors.New("-since must be earlier than -until")
	}
	cfg, err := config.Load(*configPath)
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
	if !route.RequiresAuth() {
		return fmt.Errorf("route %q has no Switchboard access policy to review", normalized)
	}
	auditDir, err := auditDirectory(*dir, *configPath)
	if err != nil {
		return err
	}
	report := policyReviewReport{
		Host:              normalized,
		Scope:             "configured identity candidates and locally recorded access decisions only; decision events are not proof of successful upstream use",
		GeneratedAt:       now,
		Since:             since,
		Until:             until,
		Candidates:        buildPolicyReviewCandidates(route),
		SubjectLimit:      *subjectLimit,
		ObservedSubjects:  make([]policyReviewSubject, 0),
		HistoryAssessment: "candidate membership cannot be joined to audit subjects: events contain opaque provider/passkey subjects, not email or group membership; no candidate is classified as unused",
	}
	if report.SubjectLimit == 0 {
		report.SubjectLimit = maxPolicyReviewSubjects
	}
	var fingerprintKey [32]byte
	if _, err := rand.Read(fingerprintKey[:]); err != nil {
		return fmt.Errorf("create per-report subject fingerprint key: %w", err)
	}
	counts := make(map[string]policyReviewSubjectCounts)
	verification, err := audit.ReadVerified(ctx, auditDir, func(record audit.Record) error {
		if !isReviewDecision(record.Type) || record.Host != normalized || record.Time.Before(since) || !record.Time.Before(until) {
			return nil
		}
		recordPolicyReviewDecision(&report, counts, record)
		return nil
	})
	if err != nil {
		return fmt.Errorf("read and verify audit events: %w", err)
	}
	report.Audit = verification
	for actor, value := range counts {
		if actor == "" {
			continue
		}
		report.ObservedSubjects = append(report.ObservedSubjects, policyReviewSubject{
			Fingerprint: subjectFingerprint(fingerprintKey[:], actor), Allowed: value.allowed, Denied: value.denied, StepUp: value.stepUp,
		})
	}
	sort.Slice(report.ObservedSubjects, func(i, j int) bool {
		if report.ObservedSubjects[i].Allowed != report.ObservedSubjects[j].Allowed {
			return report.ObservedSubjects[i].Allowed > report.ObservedSubjects[j].Allowed
		}
		return report.ObservedSubjects[i].Fingerprint < report.ObservedSubjects[j].Fingerprint
	})
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	writePolicyReviewText(out, report)
	return nil
}

func buildPolicyReviewCandidates(route config.Route) []policyReviewCandidate {
	policy := route.Policy()
	candidates := make([]policyReviewCandidate, 0, len(policy.AllowedEmails)+len(policy.AllowedDomains)+len(policy.RequiredGroups)+1)
	for _, email := range policy.AllowedEmails {
		candidates = append(candidates, policyReviewCandidate{
			Kind: "email", Value: email, MatchSemantics: "OR with allowed_domains; requires verified email",
			Usage: "unknown", Reason: "audit Actor is an opaque subject and does not identify this configured email",
		})
	}
	for _, domain := range policy.AllowedDomains {
		candidates = append(candidates, policyReviewCandidate{
			Kind: "domain", Value: domain, MatchSemantics: "OR with allowed_emails; requires verified email",
			Usage: "unknown", Reason: "domain membership and identities are not enumerated in config or decision events",
		})
	}
	for _, group := range policy.RequiredGroups {
		candidates = append(candidates, policyReviewCandidate{
			Kind: "required_group", Value: group, MatchSemantics: "AND with the email identity policy; every required group is needed",
			Usage: "unknown", Reason: "group membership is not recorded in access decision events",
		})
	}
	if len(policy.AllowedEmails) == 0 && len(policy.AllowedDomains) == 0 && len(policy.RequiredGroups) == 0 {
		candidates = append(candidates, policyReviewCandidate{
			Kind: "any_authenticated", Value: "any authenticated principal", MatchSemantics: "no email, domain, or group restriction",
			Usage: "unknown", Reason: "the eligible principal set is not enumerable from the route config",
		})
	}
	return candidates
}

func recordPolicyReviewDecision(report *policyReviewReport, counts map[string]policyReviewSubjectCounts, record audit.Record) {
	report.DecisionEvents++
	switch record.Type {
	case events.AccessAllowed:
		report.Allowed++
	case events.AccessDenied:
		report.Denied++
	case events.AccessStepUp:
		report.StepUp++
	default:
		return
	}
	if record.Actor == "" {
		report.MissingActor++
		return
	}
	value, exists := counts[record.Actor]
	if !exists && len(counts) >= report.SubjectLimit {
		report.Unattributed++
		return
	}
	switch record.Type {
	case events.AccessAllowed:
		value.allowed++
	case events.AccessDenied:
		value.denied++
	case events.AccessStepUp:
		value.stepUp++
	}
	counts[record.Actor] = value
}

func isReviewDecision(kind events.Type) bool {
	return kind == events.AccessAllowed || kind == events.AccessDenied || kind == events.AccessStepUp
}

func subjectFingerprint(key []byte, subject string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(subject))
	digest := mac.Sum(nil)
	return "subject-fingerprint:" + hex.EncodeToString(digest[:8])
}

func writePolicyReviewText(out io.Writer, report policyReviewReport) {
	fmt.Fprintf(out, "route: %s\nwindow: [%s, %s)\n", report.Host, report.Since.Format(time.RFC3339), report.Until.Format(time.RFC3339))
	fmt.Fprintf(out, "audit: records=%d internally_consistent=%t tamper_evident=%t\n", report.Audit.Records, report.Audit.InternallyConsistent, report.Audit.TamperEvident)
	fmt.Fprintf(out, "decision_events=%d allowed=%d denied=%d step_up=%d missing_actor=%d observed_subjects=%d\n",
		report.DecisionEvents, report.Allowed, report.Denied, report.StepUp, report.MissingActor, len(report.ObservedSubjects))
	for _, candidate := range report.Candidates {
		fmt.Fprintf(out, "candidate %s=%s usage=%s (%s)\n", candidate.Kind, candidate.Value, candidate.Usage, candidate.Reason)
	}
	for _, subject := range report.ObservedSubjects {
		fmt.Fprintf(out, "%s allowed=%d denied=%d step_up=%d\n", subject.Fingerprint, subject.Allowed, subject.Denied, subject.StepUp)
	}
	if report.Unattributed > 0 {
		fmt.Fprintf(out, "decision events not grouped because the subject limit was reached: %d\n", report.Unattributed)
	}
	fmt.Fprintf(out, "history: %s\n", report.HistoryAssessment)
	fmt.Fprintf(out, "scope: %s\n", report.Scope)
}
