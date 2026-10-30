package domaincheck

import "strings"

// ChallengeCheck is whether a zone's certificate challenge can be written
// (EDGE-008): the _acme-challenge CNAME the operator creates once when the
// challenge is delegated, with the exact record to create when it is not
// in place yet.
type ChallengeCheck struct {
	Step
	Record *Record `json:"record,omitempty"`
}

// AssessChallenge reads what the resolver found at name. canonical is the
// name the CNAME chain ends at ("" or name itself when there is none);
// lookupFailed marks a lookup error. A challenge that is not delegated is
// written by the DNS provider itself, so nothing is checked or asked for.
func AssessChallenge(name, target string, delegated bool, canonical string, lookupFailed bool) ChallengeCheck {
	if !delegated {
		return ChallengeCheck{Step: Step{State: StateOK, Detail: "The DNS provider writes " + name + " itself at each issuance; no record to create"}}
	}
	record := &Record{Type: "CNAME", Name: name, Content: target}
	canonical = strings.TrimSuffix(strings.ToLower(canonical), ".")
	switch {
	case lookupFailed || canonical == "" || canonical == name:
		return ChallengeCheck{Step: Step{State: StateBad, Detail: "Create " + name + " as a CNAME to " + target + " at the DNS that serves the domain"}, Record: record}
	case canonical != target:
		return ChallengeCheck{Step: Step{State: StateBad, Detail: name + " points at " + canonical + "; change it to " + target}, Record: record}
	default:
		return ChallengeCheck{Step: Step{State: StateOK, Detail: name + " points at " + target}}
	}
}
