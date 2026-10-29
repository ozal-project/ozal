package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/audit"
	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
)

type policyReplayReport struct {
	Host            string                `json:"host"`
	Scope           string                `json:"scope"`
	GeneratedAt     time.Time             `json:"generated_at"`
	Since           string                `json:"since,omitempty"`
	Until           string                `json:"until,omitempty"`
	Rule            *config.ContextRule   `json:"candidate_context_rule"`
	Audit           audit.Verification    `json:"audit_verification"`
	AccessEvents    uint64                `json:"access_events"`
	Evaluated       uint64                `json:"evaluated"`
	WouldAllow      uint64                `json:"would_allow"`
	WouldDeny       uint64                `json:"would_deny"`
	WouldStepUp     uint64                `json:"would_step_up"`
	Unknown         uint64                `json:"unknown"`
	MissingContext  uint64                `json:"missing_context"`
	Examples        []policyReplayExample `json:"examples"`
	ExamplesOmitted uint64                `json:"examples_omitted"`
}

type policyReplayExample struct {
	LogID          string    `json:"log_id"`
	Sequence       uint64    `json:"sequence"`
	Time           time.Time `json:"time"`
	Actor          string    `json:"actor,omitempty"`
	RecordedType   string    `json:"recorded_type"`
	RecordedCode   string    `json:"recorded_code,omitempty"`
	Would          string    `json:"would"`
	Reason         string    `json:"reason"`
	UnknownSignals []string  `json:"unknown_signals,omitempty"`
}

// runPolicyReplay evaluates a candidate route context rule against the
// locally retained access-decision events. It never writes the candidate to
// config, updates audit state, or changes a live decision.
func runPolicyReplay(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("policy replay", flag.ContinueOnError)
	fs.SetOutput(out)
	configPath := fs.String("config", "config.json", "candidate config containing the route context_rule")
	dir := fs.String("dir", "", "audit directory (absolute path; overrides audit.dir)")
	host := fs.String("host", "", "route host whose candidate context rule to replay")
	sinceText := fs.String("since", "", "include events at or after this RFC3339 timestamp")
	untilText := fs.String("until", "", "include events before this RFC3339 timestamp")
	exampleLimit := fs.Int("examples", 50, "maximum non-allow examples to print (0 disables the limit)")
	asJSON := fs.Bool("json", false, "print stable JSON")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: ozal policy replay -config CANDIDATE.json -host HOST [-dir ABS_PATH] [-since RFC3339] [-until RFC3339] [-examples N] [-json]")
		fmt.Fprintln(fs.Output(), "Offline context-rule-only replay. It requires a stopped writer and a locally consistent audit chain; it never changes live policy.")
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
	if *exampleLimit < 0 || *exampleLimit > 10_000 {
		return errors.New("-examples must be between 0 and 10000")
	}
	var since, until time.Time
	var err error
	if *sinceText != "" {
		since, err = time.Parse(time.RFC3339Nano, *sinceText)
		if err != nil {
			return fmt.Errorf("-since must be RFC3339: %w", err)
		}
	}
	if *untilText != "" {
		until, err = time.Parse(time.RFC3339Nano, *untilText)
		if err != nil {
			return fmt.Errorf("-until must be RFC3339: %w", err)
		}
	}
	if !since.IsZero() && !until.IsZero() && !since.Before(until) {
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
	if route.Access == nil || route.Access.ContextRule == nil {
		return fmt.Errorf("route %q has no candidate access.context_rule", normalized)
	}
	auditDir, err := auditDirectory(*dir, *configPath)
	if err != nil {
		return err
	}
	rule := *route.Access.ContextRule
	report := policyReplayReport{
		Host: normalized, Scope: "context_rule_only; identity and other pipeline stages are not replayed",
		GeneratedAt: time.Now().UTC(), Rule: &rule,
		Examples: make([]policyReplayExample, 0),
	}
	if sinceText != nil && *sinceText != "" {
		report.Since = since.UTC().Format(time.RFC3339Nano)
	}
	if untilText != nil && *untilText != "" {
		report.Until = until.UTC().Format(time.RFC3339Nano)
	}
	verification, err := audit.ReadVerified(ctx, auditDir, func(record audit.Record) error {
		if !isReplayDecision(record.Type) || record.Host != normalized {
			return nil
		}
		if !since.IsZero() && record.Time.Before(since) || !until.IsZero() && !record.Time.Before(until) {
			return nil
		}
		report.AccessEvents++
		if record.Context == nil {
			report.MissingContext++
			report.Unknown++
			report.appendExample(policyReplayExample{
				LogID: record.LogID, Sequence: record.Seq, Time: record.Time,
				Actor: record.Actor, RecordedType: string(record.Type), RecordedCode: record.Code,
				Would: "unknown", Reason: "context was not recorded",
				UnknownSignals: []string{"context (source: not_recorded)"},
			}, *exampleLimit)
			return nil
		}
		report.Evaluated++
		evaluation := auth.EvaluateContextRule(*record.Context, &rule)
		decision := evaluation.Decision
		example := policyReplayExample{
			LogID: record.LogID, Sequence: record.Seq, Time: record.Time,
			Actor: record.Actor, RecordedType: string(record.Type), RecordedCode: record.Code,
			Reason: decision.Reason, UnknownSignals: evaluation.UnknownSignals,
		}
		switch decision.Code {
		case auth.CodeContextSignalUnknown:
			example.Would = "unknown"
			report.Unknown++
		case auth.CodeContextRuleDenied:
			example.Would = "deny"
			report.WouldDeny++
		case auth.CodeContextStepUpRequired:
			example.Would = "step_up"
			report.WouldStepUp++
		default:
			example.Would = "allow"
			report.WouldAllow++
		}
		if example.Would != "allow" {
			report.appendExample(example, *exampleLimit)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("read and verify audit events: %w", err)
	}
	report.Audit = verification
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	writePolicyReplayText(out, report)
	return nil
}

func (r *policyReplayReport) appendExample(example policyReplayExample, limit int) {
	if limit > 0 && len(r.Examples) >= limit {
		r.ExamplesOmitted++
		return
	}
	r.Examples = append(r.Examples, example)
}

func isReplayDecision(kind events.Type) bool {
	return kind == events.AccessAllowed || kind == events.AccessDenied || kind == events.AccessStepUp
}

func writePolicyReplayText(out io.Writer, report policyReplayReport) {
	fmt.Fprintf(out, "route: %s\nscope: %s\n", report.Host, report.Scope)
	fmt.Fprintf(out, "audit: records=%d internally_consistent=%t tamper_evident=%t\n", report.Audit.Records, report.Audit.InternallyConsistent, report.Audit.TamperEvident)
	fmt.Fprintf(out, "window: since=%s until=%s\n", valueOr(report.Since, "beginning"), valueOr(report.Until, "now"))
	fmt.Fprintf(out, "events=%d evaluated=%d missing_context=%d would_allow=%d would_deny=%d would_step_up=%d unknown=%d\n",
		report.AccessEvents, report.Evaluated, report.MissingContext, report.WouldAllow, report.WouldDeny, report.WouldStepUp, report.Unknown)
	for _, example := range report.Examples {
		fmt.Fprintf(out, "event %s:%d actor=%s recorded=%s/%s would=%s reason=%s",
			example.LogID, example.Sequence, valueOr(example.Actor, "unknown"), example.RecordedType,
			example.RecordedCode, example.Would, example.Reason)
		if len(example.UnknownSignals) > 0 {
			fmt.Fprintf(out, " unknown_signals=[%s]", strings.Join(example.UnknownSignals, ", "))
		}
		fmt.Fprintln(out)
	}
	if report.ExamplesOmitted > 0 {
		fmt.Fprintf(out, "examples omitted: %d (increase -examples to include more)\n", report.ExamplesOmitted)
	}
	fmt.Fprintln(out, "shadow only: no live policy or audit record was changed")
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
