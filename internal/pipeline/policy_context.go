package pipeline

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/policycontext"
)

// RequestPolicyContext converts request and session evidence at the HTTP
// boundary. It records classifications and provenance, never the raw path,
// query string or client IP.
func RequestPolicyContext(req *http.Request, route string, principal *auth.Principal, observedAt time.Time, catalog *config.RouteResource) policycontext.Context {
	input := policycontext.Input{
		ObservedAt:      observedAt,
		Owner:           policycontext.Unknown[string](policycontext.SourceUnconfigured),
		DataClass:       policycontext.Unknown[string](policycontext.SourceUnconfigured),
		Sensitivity:     policycontext.Unknown[string](policycontext.SourceUnconfigured),
		DeviceFirstSeen: policycontext.Unknown[bool](policycontext.SourceNotCollected),
	}
	if catalog != nil {
		input.Owner = policycontext.Known(catalog.Owner, policycontext.SourceRouteConfig)
		input.DataClass = policycontext.Known(catalog.DataClass, policycontext.SourceRouteConfig)
		input.Sensitivity = policycontext.Known(catalog.Sensitivity, policycontext.SourceRouteConfig)
	}
	if route != "" {
		input.Route = policycontext.Known(route, policycontext.SourceRouteTable)
	} else {
		input.Route = policycontext.Unknown[string](policycontext.SourceNotRecorded)
	}
	if principal == nil {
		input.SessionMethod = policycontext.Unknown[string](policycontext.SourceNoSession)
		input.SessionAuthTime = policycontext.Unknown[time.Time](policycontext.SourceNoSession)
		input.SessionUserVerified = policycontext.Unknown[bool](policycontext.SourceNoSession)
		input.SessionDeviceBound = policycontext.Unknown[bool](policycontext.SourceNoSession)
		input.DeviceFirstSeen = policycontext.Unknown[bool](policycontext.SourceNoSession)
	} else {
		if principal.Method != "" {
			input.SessionMethod = policycontext.Known(principal.Method, policycontext.SourceSession)
		} else {
			input.SessionMethod = policycontext.Unknown[string](policycontext.SourceNotRecorded)
		}
		if !principal.AuthTime.IsZero() {
			input.SessionAuthTime = policycontext.Known(principal.AuthTime, policycontext.SourceSession)
		} else {
			input.SessionAuthTime = policycontext.Unknown[time.Time](policycontext.SourceNotRecorded)
		}
		if principal.Method == auth.MethodPasskey && principal.UserVerified != nil {
			input.SessionUserVerified = policycontext.Known(*principal.UserVerified, policycontext.SourceSession)
		} else {
			input.SessionUserVerified = policycontext.Unknown[bool](policycontext.SourceNotRecorded)
		}
		if principal.Method != "" {
			input.SessionDeviceBound = policycontext.Known(principal.DeviceBound, policycontext.SourceSession)
		} else {
			input.SessionDeviceBound = policycontext.Unknown[bool](policycontext.SourceNotRecorded)
		}
		if principal.Method == auth.MethodPasskey && principal.DeviceFirstSeen != nil {
			input.DeviceFirstSeen = policycontext.Known(*principal.DeviceFirstSeen, policycontext.SourceSession)
		} else {
			input.DeviceFirstSeen = policycontext.Unknown[bool](policycontext.SourceNotRecorded)
		}
	}
	var trust TrustEvidence
	var hasTrust bool
	if req != nil {
		trust, hasTrust = TrustEvidenceFrom(req.Context())
	}
	if hasTrust {
		if trust.ArrivalPath != "" {
			input.ArrivalPath = policycontext.Known(trust.ArrivalPath, policycontext.SourceTrustStage)
		} else {
			input.ArrivalPath = policycontext.Unknown[string](policycontext.SourceNotRecorded)
		}
		if trust.VisitorIPSource != "" {
			input.VisitorIPSource = policycontext.Known(trust.VisitorIPSource, policycontext.SourceTrustStage)
		} else {
			input.VisitorIPSource = policycontext.Unknown[string](policycontext.SourceNotRecorded)
		}
	} else {
		input.ArrivalPath = policycontext.Unknown[string](policycontext.SourceNotCollected)
		input.VisitorIPSource = policycontext.Unknown[string](policycontext.SourceNotCollected)
	}
	if req != nil {
		if req.Method != "" {
			input.HTTPMethod = policycontext.Known(req.Method, policycontext.SourceHTTPRequest)
			if catalog != nil && catalog.RiskyActions != nil {
				input.Risky = policycontext.Known(slices.Contains(catalog.RiskyActions, req.Method), policycontext.SourceRouteConfig)
			} else {
				input.Risky = policycontext.Unknown[bool](policycontext.SourceUnconfigured)
			}
		} else {
			input.HTTPMethod = policycontext.Unknown[string](policycontext.SourceNotRecorded)
			input.Risky = policycontext.Unknown[bool](policycontext.SourceNotRecorded)
		}
		if req.URL != nil {
			input.PathClass = policycontext.Known(classifyRequestPath(req.URL.Path), policycontext.SourceClassifier)
		} else {
			input.PathClass = policycontext.Unknown[string](policycontext.SourceNotRecorded)
		}
	} else {
		input.HTTPMethod = policycontext.Unknown[string](policycontext.SourceNotRecorded)
		input.PathClass = policycontext.Unknown[string](policycontext.SourceNotRecorded)
		input.Risky = policycontext.Unknown[bool](policycontext.SourceNotRecorded)
	}
	return policycontext.Build(input)
}

func classifyRequestPath(path string) string {
	switch {
	case pathUnder(path, "/_/auth"):
		return policycontext.PathAuthentication
	case pathUnder(path, "/_/api"):
		return policycontext.PathControlAPI
	case pathUnder(path, "/_/"):
		return policycontext.PathControlPage
	default:
		return policycontext.PathApplication
	}
}

func pathUnder(path, prefix string) bool {
	base := strings.TrimSuffix(prefix, "/")
	return path == base || strings.HasPrefix(path, base+"/")
}
