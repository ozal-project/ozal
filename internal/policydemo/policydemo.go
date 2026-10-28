// Package policydemo is the project page's "ask the real engine" demo: the
// same decision auth.NewReport gives the CLI and the console simulator, behind
// one JSON-in, JSON-out function that cmd/policywasm compiles to WebAssembly.
// It adds no policy logic of its own, only input checks for text typed into a
// web page. Nothing here reaches a network, a file or a clock the report does
// not already read.
package policydemo

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/switchboard-project/switchboard/internal/auth"
	"github.com/switchboard-project/switchboard/internal/config"
)

const (
	maxInput  = 64 << 10
	maxGroups = 64
)

type principal struct {
	Email           string   `json:"email"`
	EmailVerified   bool     `json:"email_verified"`
	Groups          []string `json:"groups"`
	Method          string   `json:"method"`
	DeviceBound     *bool    `json:"device_bound"`
	UserVerified    *bool    `json:"user_verified"`
	DeviceFirstSeen *bool    `json:"device_first_seen"`
}

type request struct {
	Route         config.Route `json:"route"`
	Principal     *principal   `json:"principal"`
	RequestMethod string       `json:"request_method"`
}

// Explain evaluates one route and an optional person and returns the same
// auth.Report the console and `switchboard policy explain -json` return. A nil
// principal simulates a request without a session.
func Explain(input []byte) ([]byte, error) {
	if len(input) > maxInput {
		return nil, errors.New("input is too large")
	}
	var req request
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, errors.New("input is not valid JSON of the expected shape")
	}
	host, err := config.NormalizeHost(req.Route.Host)
	if err != nil {
		return nil, fmt.Errorf("route host: %w", err)
	}
	// The demo evaluates a decision; it never forwards, so no target is kept.
	route := config.Route{Host: host, AuthRequired: req.Route.AuthRequired, Access: req.Route.Access}

	var who *auth.Principal
	if p := req.Principal; p != nil {
		if p.Email == "" {
			return nil, errors.New("principal email is required; send no principal to simulate no session")
		}
		if len(p.Groups) > maxGroups {
			return nil, fmt.Errorf("principal groups: at most %d", maxGroups)
		}
		if p.Method != "" && !slices.Contains([]string{auth.MethodPasskey, auth.MethodOIDC}, p.Method) {
			return nil, errors.New("principal method must be passkey or oidc")
		}
		if (p.DeviceBound != nil || p.UserVerified != nil || p.DeviceFirstSeen != nil) && p.Method != auth.MethodPasskey {
			return nil, errors.New("device_bound, user_verified and device_first_seen need method passkey")
		}
		who = &auth.Principal{
			Email:           p.Email,
			EmailVerified:   p.EmailVerified,
			Groups:          p.Groups,
			Method:          p.Method,
			DeviceBound:     p.DeviceBound != nil && *p.DeviceBound,
			UserVerified:    p.UserVerified,
			DeviceFirstSeen: p.DeviceFirstSeen,
		}
	}
	if req.RequestMethod != "" && !config.IsSupportedHTTPMethod(req.RequestMethod) {
		return nil, errors.New("request_method is not a supported HTTP method")
	}
	return json.Marshal(auth.NewReportWithRequestMethod(host, route, who, req.RequestMethod))
}
