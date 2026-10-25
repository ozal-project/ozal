package policycontext

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildKeepsUnknownSeparateFromFalseAndZero(t *testing.T) {
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	knownAuthTime := time.Time{}
	got := Build(Input{
		ObservedAt:         at,
		SessionMethod:      Known("oidc", SourceSession),
		SessionAuthTime:    Known(knownAuthTime, SourceSession),
		SessionDeviceBound: Known(false, SourceSession),
		ArrivalPath:        Known(ArrivalDirect, SourceTrustStage),
		VisitorIPSource:    Known(IPSourcePeer, SourceTrustStage),
		Route:              Known("admin.example.com", SourceRouteTable),
		Owner:              Known("platform", SourceRouteConfig),
		DataClass:          Known("account_data", SourceRouteConfig),
		Sensitivity:        Unknown[string](SourceUnconfigured),
		HTTPMethod:         Known("GET", SourceHTTPRequest),
		PathClass:          Known(PathApplication, SourceClassifier),
		Risky:              Known(false, SourceRouteConfig),
		DeviceFirstSeen:    Unknown[bool](SourceNotCollected),
	})

	if got.Version != Version {
		t.Fatalf("version = %d, want %d", got.Version, Version)
	}
	if got.Session.Method.Value == nil || *got.Session.Method.Value != "oidc" {
		t.Fatalf("method signal = %+v", got.Session.Method)
	}
	if got.Session.AuthTime.Value == nil || !got.Session.AuthTime.Value.IsZero() {
		t.Fatalf("zero auth time must remain a known value: %+v", got.Session.AuthTime)
	}
	if got.Session.DeviceBound.Value == nil || *got.Session.DeviceBound.Value {
		t.Fatalf("known false device_bound became unknown or true: %+v", got.Session.DeviceBound)
	}
	if got.Session.UserVerified.Value != nil || got.Session.UserVerified.Source != SourceUnknown {
		t.Fatalf("missing user verification should be unknown: %+v", got.Session.UserVerified)
	}
	if got.Resource.Sensitivity.Value != nil || got.Resource.Sensitivity.Source != SourceUnconfigured {
		t.Fatalf("unconfigured sensitivity should be explicit: %+v", got.Resource.Sensitivity)
	}
	if got.Resource.Owner.Value == nil || *got.Resource.Owner.Value != "platform" || got.Resource.DataClass.Value == nil || *got.Resource.DataClass.Value != "account_data" {
		t.Fatalf("route catalog labels were lost: %+v", got.Resource)
	}
	if got.Action.Risky.Value == nil || *got.Action.Risky.Value {
		t.Fatalf("known non-risky action became unknown or risky: %+v", got.Action.Risky)
	}
	if got.History.DeviceFirstSeen.Value != nil || got.History.DeviceFirstSeen.Source != SourceNotCollected {
		t.Fatalf("uncollected history should be explicit: %+v", got.History.DeviceFirstSeen)
	}
	for _, observedAt := range []time.Time{
		got.Session.Method.ObservedAt,
		got.Session.UserVerified.ObservedAt,
		got.Network.ArrivalPath.ObservedAt,
		got.Resource.Owner.ObservedAt,
		got.Resource.DataClass.ObservedAt,
		got.Resource.Sensitivity.ObservedAt,
		got.Action.PathClass.ObservedAt,
		got.Action.Risky.ObservedAt,
		got.History.DeviceFirstSeen.ObservedAt,
	} {
		if !observedAt.Equal(at) {
			t.Fatalf("signal observed at %v, want %v", observedAt, at)
		}
	}
}

func TestBuildDoesNotAcceptAnUnsourcedValueAsKnown(t *testing.T) {
	at := time.Now().UTC()
	got := Build(Input{ObservedAt: at, Route: Evidence[string]{Value: "admin.example.com", Known: true}})
	if got.Resource.Route.Value != nil || got.Resource.Route.Source != SourceUnknown {
		t.Fatalf("unsourced route was presented as evidence: %+v", got.Resource.Route)
	}
}

func TestBuildMarksEveryMissingSignalUnknownWithSourceAndTime(t *testing.T) {
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	got := Build(Input{ObservedAt: at})
	checkUnknown(t, "session.method", got.Session.Method, at)
	checkUnknown(t, "session.auth_time", got.Session.AuthTime, at)
	checkUnknown(t, "session.user_verified", got.Session.UserVerified, at)
	checkUnknown(t, "session.device_bound", got.Session.DeviceBound, at)
	checkUnknown(t, "network.arrival_path", got.Network.ArrivalPath, at)
	checkUnknown(t, "network.visitor_ip_source", got.Network.VisitorIPSource, at)
	checkUnknown(t, "resource.route", got.Resource.Route, at)
	checkUnknown(t, "resource.owner", got.Resource.Owner, at)
	checkUnknown(t, "resource.data_class", got.Resource.DataClass, at)
	checkUnknown(t, "resource.sensitivity", got.Resource.Sensitivity, at)
	checkUnknown(t, "action.http_method", got.Action.HTTPMethod, at)
	checkUnknown(t, "action.path_class", got.Action.PathClass, at)
	checkUnknown(t, "action.risky", got.Action.Risky, at)
	checkUnknown(t, "history.device_first_seen", got.History.DeviceFirstSeen, at)
}

func checkUnknown[T any](t *testing.T, name string, signal Signal[T], wantAt time.Time) {
	t.Helper()
	if signal.Value != nil || signal.Source == "" || !signal.ObservedAt.Equal(wantAt) {
		t.Errorf("%s = %+v, want unknown with source and observation time %v", name, signal, wantAt)
	}
}

func TestContextJSONIncludesUnknownsAndNoSensitiveRequestFields(t *testing.T) {
	got := Build(Input{
		ObservedAt:  time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		HTTPMethod:  Known("GET", SourceHTTPRequest),
		PathClass:   Known(PathApplication, SourceClassifier),
		Sensitivity: Unknown[string](SourceUnconfigured),
	})
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal context: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"version":1`, `"source":"unconfigured"`, `"value":null`, `"observed_at":"2026-10-25T12:00:00Z"`} {
		if !strings.Contains(text, want) {
			t.Errorf("context JSON missing %s: %s", want, text)
		}
	}
}

func FuzzBuild(f *testing.F) {
	f.Add("GET", "/secret/path", "peer", true, int64(0))
	f.Add("\x00\r\n", "/_/auth/callback", "", false, int64(-1))

	f.Fuzz(func(t *testing.T, method, pathClass, source string, known bool, unixNanos int64) {
		at := time.Unix(0, unixNanos).UTC()
		var methodEvidence Evidence[string]
		if known {
			methodEvidence = Known(method, source)
		} else {
			methodEvidence = Unknown[string](source)
		}
		got := Build(Input{
			ObservedAt:  at,
			HTTPMethod:  methodEvidence,
			PathClass:   Known(pathClass, SourceClassifier),
			Sensitivity: Unknown[string](SourceUnconfigured),
		})
		if got.Version != Version || got.Action.HTTPMethod.Source == "" || got.Action.PathClass.Source == "" {
			t.Fatalf("Build returned incomplete context: %+v", got)
		}
		if known && source != "" && (got.Action.HTTPMethod.Value == nil || *got.Action.HTTPMethod.Value != method) {
			t.Fatalf("known method was lost: %+v", got.Action.HTTPMethod)
		}
		if got.Action.PathClass.Value != nil && *got.Action.PathClass.Value != pathClass {
			t.Fatalf("path class was changed: %+v", got.Action.PathClass)
		}
	})
}
