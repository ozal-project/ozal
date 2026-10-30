// Package zonesetup decides what a domain still needs at its DNS provider
// and proxy before its addresses can work through this edge (EDGE-009).
//
// Plan is the pure core: it reads what was observed and returns one step per
// requirement, each with a state, the action an API can take and the words
// an operator needs to do it by hand. It never decides to replace something
// it did not create: a challenge record that points elsewhere is reported,
// not overwritten.
package zonesetup

// StepName identifies one requirement.
type StepName string

const (
	// Challenge is the CNAME that delegates the zone's DNS-01 challenge.
	Challenge StepName = "challenge"
	// Encryption is the proxy's connection mode to this edge.
	Encryption StepName = "encryption"
	// OriginPull is the client certificate the proxy presents to this edge.
	OriginPull StepName = "origin_pull"
)

// State is where a step stands.
type State string

const (
	Done      State = "done"
	Todo      State = "todo"       // missing; Action can do it, or Manual says how
	Blocked   State = "blocked"    // something else is there; only the operator may change it
	Unknown   State = "unknown"    // cannot be read without a provider API
	NotNeeded State = "not_needed" // does not apply to this edge
)

// Action is what Apply does for a Todo step. Empty means only the operator can.
type Action string

const (
	CreateCNAME    Action = "create_cname"
	SetStrict      Action = "set_strict"
	UploadIdentity Action = "upload_certificate"
	EnablePull     Action = "enable_origin_pull"
)

// Observed is what the edge read about one zone.
type Observed struct {
	Zone string
	// Provider is true when a provider API answered for this zone; the
	// Encryption and OriginPull facts below are only meaningful then.
	Provider bool
	// Proxied is the edge's own setting: visitors arrive through the
	// provider's proxy. Without it no proxy setting applies.
	Proxied bool

	ChallengeDelegated bool
	ChallengeName      string
	ChallengeTarget    string
	// ChallengeCurrent is the CNAME found at ChallengeName ("" for none);
	// ChallengeRead is false when it could not be looked up.
	ChallengeCurrent string
	ChallengeRead    bool

	// SSLMode is the proxy's mode: off, flexible, full or strict.
	SSLMode string
	// OriginAuth is true when this edge refuses connections without a
	// client certificate from its trust bundle.
	OriginAuth bool
	// PullEnabled: the zone-level setting is on. PullTrusted: the
	// certificate the proxy would present passes this edge's policy.
	PullEnabled bool
	PullTrusted bool
}

// Step is one requirement with its outcome.
type Step struct {
	Name   StepName `json:"name"`
	State  State    `json:"state"`
	Action Action   `json:"action,omitempty"`
	Title  string   `json:"title"`
	Detail string   `json:"detail"`
	// Manual is how to do it at the provider's dashboard, for a Todo or
	// Unknown step the edge cannot do itself.
	Manual string `json:"manual,omitempty"`
}

// Ready reports whether nothing is left to do.
func Ready(steps []Step) bool {
	for _, s := range steps {
		if s.State != Done && s.State != NotNeeded {
			return false
		}
	}
	return true
}

// Plan returns the steps in the order Apply runs them.
func Plan(o Observed) []Step {
	return []Step{challenge(o), encryption(o), originPull(o)}
}

func challenge(o Observed) Step {
	s := Step{Name: Challenge, Title: "DNS record for certificates"}
	switch {
	case !o.ChallengeDelegated:
		s.State, s.Detail = NotNeeded, "The edge writes its certificate checks through its own DNS provider."
	case !o.ChallengeRead:
		s.State, s.Detail = Unknown, "The record could not be looked up just now."
		s.Manual = "Create a CNAME record " + o.ChallengeName + " pointing to " + o.ChallengeTarget + ", without the proxy."
	case o.ChallengeCurrent == o.ChallengeTarget:
		s.State, s.Detail = Done, "In place."
	case o.ChallengeCurrent != "":
		s.State = Blocked
		s.Detail = o.ChallengeName + " points to " + o.ChallengeCurrent + ". Ozal never replaces a record it did not create."
		s.Manual = "Change " + o.ChallengeName + " to point to " + o.ChallengeTarget + "."
	default:
		s.State, s.Detail = Todo, "Missing."
		s.Manual = "Create a CNAME record " + o.ChallengeName + " pointing to " + o.ChallengeTarget + ", without the proxy."
		if o.Provider {
			s.Action = CreateCNAME
		}
	}
	return s
}

func encryption(o Observed) Step {
	s := Step{Name: Encryption, Title: "Encrypted connection from the proxy"}
	manual := "SSL/TLS → Overview: choose Full (strict)."
	switch {
	case !o.Proxied:
		s.State, s.Detail = NotNeeded, "Visitors reach this edge directly; no proxy setting applies."
	case !o.Provider:
		s.State, s.Detail, s.Manual = Unknown, "Needs Full (strict): the proxy must connect over HTTPS and check this edge's certificate.", manual
	case o.SSLMode == "strict":
		s.State, s.Detail = Done, "Full (strict)."
	case o.SSLMode == "full":
		s.State, s.Detail = Done, "Full. Full (strict) also checks this edge's certificate; switch to it when every address in the zone has a valid one."
	case o.SSLMode == "off" || o.SSLMode == "flexible":
		s.State, s.Action, s.Manual = Todo, SetStrict, manual
		s.Detail = "The proxy talks to this edge without HTTPS, which it does not accept: visitors get an error."
	default:
		s.State, s.Detail, s.Manual = Unknown, "The proxy reported an unknown mode.", manual
	}
	return s
}

func originPull(o Observed) Step {
	s := Step{Name: OriginPull, Title: "Proxy certificate for this edge"}
	manual := "SSL/TLS → Origin Server → Authenticated Origin Pulls: upload a client certificate this edge trusts at Zone-level and turn Zone-level on."
	switch {
	case !o.OriginAuth:
		s.State, s.Detail = NotNeeded, "This edge does not ask the proxy for a certificate."
	case !o.Proxied:
		s.State = Blocked
		s.Detail = "This edge only accepts connections that present its proxy certificate, but its records are not proxied."
	case !o.Provider:
		s.State, s.Detail, s.Manual = Unknown, "This edge only accepts the proxy when it presents a certificate it trusts; without it visitors get error 520.", manual
	case o.PullTrusted && o.PullEnabled:
		s.State, s.Detail = Done, "The proxy presents a certificate this edge trusts."
	case o.PullTrusted:
		s.State, s.Action, s.Manual = Todo, EnablePull, manual
		s.Detail = "The certificate is uploaded but Zone-level is off."
	default:
		s.State, s.Action, s.Manual = Todo, UploadIdentity, manual
		s.Detail = "The proxy has no certificate this edge trusts: visitors get error 520."
	}
	return s
}
