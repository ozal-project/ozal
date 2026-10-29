// Package policycontext defines the evidence recorded beside an access
// decision. Unknown values stay unknown; this package does not infer risk or
// make authorization decisions.
package policycontext

import "time"

const (
	Version = 1

	SourceUnknown             = "unknown"
	SourceNotRecorded         = "not_recorded"
	SourceNotCollected        = "not_collected"
	SourceUnconfigured        = "unconfigured"
	SourceNoSession           = "no_session"
	SourceSimulationNoRequest = "simulation_no_request"
	SourceSession             = "session"
	SourceTrustStage          = "trust_stage"
	SourceRouteTable          = "route_table"
	SourceRouteConfig         = "route_config"
	SourceHTTPRequest         = "http_request"
	SourceSimulationInput     = "simulation_input"
	SourceClassifier          = "ozal_classifier"

	ArrivalDirect  = "direct"
	ArrivalProxy   = "trusted_proxy"
	ArrivalTunnel  = "authenticated_tunnel"
	IPSourcePeer   = "transport_peer"
	IPSourceProxy  = "trusted_proxy_header"
	IPSourceTunnel = "tunnel_assertion"

	PathApplication    = "application"
	PathAuthentication = "authentication"
	PathControlAPI     = "control_api"
	PathControlPage    = "control_page"
)

// Evidence is a value and the provenance needed to interpret it. A zero
// Evidence is unknown; a known value without a source is also treated as
// unknown by Build.
type Evidence[T any] struct {
	Value  T
	Known  bool
	Source string
}

// Known reports an observed value from source.
func Known[T any](value T, source string) Evidence[T] {
	return Evidence[T]{Value: value, Known: true, Source: source}
}

// Unknown reports that the value could not be established. source may name
// the reason, such as not_recorded or unconfigured.
func Unknown[T any](source string) Evidence[T] {
	return Evidence[T]{Source: source}
}

// Signal carries one typed value, its source, and when it was observed. A nil
// Value is the only unknown representation; false, zero and empty strings can
// still be known values.
type Signal[T any] struct {
	Value      *T        `json:"value"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
}

type Session struct {
	Method       Signal[string]    `json:"method"`
	AuthTime     Signal[time.Time] `json:"auth_time"`
	UserVerified Signal[bool]      `json:"user_verified"`
	DeviceBound  Signal[bool]      `json:"device_bound"`
}

type Network struct {
	ArrivalPath     Signal[string] `json:"arrival_path"`
	VisitorIPSource Signal[string] `json:"visitor_ip_source"`
}

type Resource struct {
	Route       Signal[string] `json:"route"`
	Owner       Signal[string] `json:"owner"`
	DataClass   Signal[string] `json:"data_class"`
	Sensitivity Signal[string] `json:"sensitivity"`
}

type Action struct {
	HTTPMethod Signal[string] `json:"http_method"`
	PathClass  Signal[string] `json:"path_class"`
	Risky      Signal[bool]   `json:"risky"`
}

type History struct {
	DeviceFirstSeen Signal[bool] `json:"device_first_seen"`
}

// Context is a versioned, evidence-only snapshot used to explain an access
// decision. It intentionally contains no raw request path, query, IP address,
// body, cookie or credential.
type Context struct {
	Version  int      `json:"version"`
	Session  Session  `json:"session"`
	Network  Network  `json:"network"`
	Resource Resource `json:"resource"`
	Action   Action   `json:"action"`
	History  History  `json:"history"`
}

// Input is the evidence collected at one request boundary. ObservedAt is
// supplied by the imperative shell so Build remains deterministic.
type Input struct {
	ObservedAt time.Time

	SessionMethod       Evidence[string]
	SessionAuthTime     Evidence[time.Time]
	SessionUserVerified Evidence[bool]
	SessionDeviceBound  Evidence[bool]

	ArrivalPath     Evidence[string]
	VisitorIPSource Evidence[string]

	Route       Evidence[string]
	Owner       Evidence[string]
	DataClass   Evidence[string]
	Sensitivity Evidence[string]

	HTTPMethod Evidence[string]
	PathClass  Evidence[string]
	Risky      Evidence[bool]

	DeviceFirstSeen Evidence[bool]
}

// Build creates a complete context snapshot. Missing or unsourced evidence is
// explicit in the result and shares the request observation time.
func Build(input Input) Context {
	at := input.ObservedAt
	return Context{
		Version: Version,
		Session: Session{
			Method:       signal(input.SessionMethod, at),
			AuthTime:     signal(input.SessionAuthTime, at),
			UserVerified: signal(input.SessionUserVerified, at),
			DeviceBound:  signal(input.SessionDeviceBound, at),
		},
		Network: Network{
			ArrivalPath:     signal(input.ArrivalPath, at),
			VisitorIPSource: signal(input.VisitorIPSource, at),
		},
		Resource: Resource{
			Route:       signal(input.Route, at),
			Owner:       signal(input.Owner, at),
			DataClass:   signal(input.DataClass, at),
			Sensitivity: signal(input.Sensitivity, at),
		},
		Action: Action{
			HTTPMethod: signal(input.HTTPMethod, at),
			PathClass:  signal(input.PathClass, at),
			Risky:      signal(input.Risky, at),
		},
		History: History{DeviceFirstSeen: signal(input.DeviceFirstSeen, at)},
	}
}

func signal[T any](evidence Evidence[T], observedAt time.Time) Signal[T] {
	if !evidence.Known || evidence.Source == "" {
		source := evidence.Source
		if source == "" {
			source = SourceUnknown
		}
		return Signal[T]{Source: source, ObservedAt: observedAt}
	}
	value := evidence.Value
	return Signal[T]{Value: &value, Source: evidence.Source, ObservedAt: observedAt}
}
