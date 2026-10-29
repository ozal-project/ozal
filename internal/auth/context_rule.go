package auth

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/policycontext"
)

// ContextRuleEvaluation is the pure result of evaluating a context rule. Its
// separate signal lists let offline replay explain unknowns without parsing a
// human-readable reason string.
type ContextRuleEvaluation struct {
	Decision       Decision
	UnknownSignals []string
	Mismatches     []string
	Matches        []string
}

// EvaluateContextRule uses equality predicates joined with AND. It requires
// every selected signal to be both present and sourced. This is deliberately
// small: policy never guesses what a missing device or request signal means.
func EvaluateContextRule(context policycontext.Context, rule *config.ContextRule) ContextRuleEvaluation {
	if rule == nil {
		return ContextRuleEvaluation{Decision: Decision{Allowed: true, Code: CodeAllowed, Reason: "allowed"}}
	}
	conditions := rule.When
	unknown := make([]string, 0, 4)
	mismatches := make([]string, 0, 4)
	matches := make([]string, 0, 4)
	checkString("session.method", conditions.SessionMethod, context.Session.Method, &unknown, &mismatches, &matches)
	checkBool("session.device_bound", conditions.SessionDeviceBound, context.Session.DeviceBound, &unknown, &mismatches, &matches)
	checkString("resource.sensitivity", conditions.ResourceSensitivity, context.Resource.Sensitivity, &unknown, &mismatches, &matches)
	checkBool("action.risky", conditions.ActionRisky, context.Action.Risky, &unknown, &mismatches, &matches)
	checkBool("history.device_first_seen", conditions.DeviceFirstSeen, context.History.DeviceFirstSeen, &unknown, &mismatches, &matches)

	if len(unknown) > 0 {
		return ContextRuleEvaluation{UnknownSignals: unknown, Mismatches: mismatches, Matches: matches, Decision: Decision{
			Code:   CodeContextSignalUnknown,
			Reason: "context rule requires unknown signal(s): " + strings.Join(unknown, "; "),
		}}
	}
	if len(mismatches) > 0 {
		return ContextRuleEvaluation{UnknownSignals: unknown, Mismatches: mismatches, Matches: matches, Decision: Decision{
			Allowed: true,
			Code:    CodeAllowed,
			Reason:  "allowed; context rule did not match: " + strings.Join(mismatches, "; "),
		}}
	}
	matched := "context rule matched: " + strings.Join(matches, ", ")
	switch rule.Then {
	case "deny":
		return ContextRuleEvaluation{UnknownSignals: unknown, Mismatches: mismatches, Matches: matches, Decision: Decision{Code: CodeContextRuleDenied, Reason: matched + "; request denied"}}
	case "step_up":
		return ContextRuleEvaluation{UnknownSignals: unknown, Mismatches: mismatches, Matches: matches, Decision: Decision{StepUp: true, Code: CodeContextStepUpRequired, Reason: matched + "; passkey step-up required"}}
	default:
		// Config validation rejects this. Keep direct callers fail closed too.
		return ContextRuleEvaluation{UnknownSignals: unknown, Mismatches: mismatches, Matches: matches, Decision: Decision{Code: CodeContextRuleDenied, Reason: matched + "; unsupported action, request denied"}}
	}
}

func evaluateContextRule(context policycontext.Context, rule *config.ContextRule) Decision {
	return EvaluateContextRule(context, rule).Decision
}

func checkString(name, expected string, signal policycontext.Signal[string], unknown, mismatches, matches *[]string) {
	if expected == "" {
		return
	}
	if signal.Value == nil || signal.Source == "" {
		*unknown = append(*unknown, unknownDescription(name, signal.Source))
		return
	}
	if *signal.Value != expected {
		*mismatches = append(*mismatches, fmt.Sprintf("%s=%q (requires %q)", name, *signal.Value, expected))
		return
	}
	*matches = append(*matches, fmt.Sprintf("%s=%q", name, expected))
}

func checkBool(name string, expected *bool, signal policycontext.Signal[bool], unknown, mismatches, matches *[]string) {
	if expected == nil {
		return
	}
	if signal.Value == nil || signal.Source == "" {
		*unknown = append(*unknown, unknownDescription(name, signal.Source))
		return
	}
	if *signal.Value != *expected {
		*mismatches = append(*mismatches, fmt.Sprintf("%s=%t (requires %t)", name, *signal.Value, *expected))
		return
	}
	*matches = append(*matches, fmt.Sprintf("%s=%t", name, *expected))
}

func unknownDescription(name, source string) string {
	if source == "" {
		source = policycontext.SourceUnknown
	}
	if len(source) > 64 || strings.IndexFunc(source, unicode.IsControl) >= 0 {
		source = "invalid_source"
	}
	return fmt.Sprintf("%s (source: %s)", name, source)
}
