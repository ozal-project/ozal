package cloudflare

import "strings"

// TunnelStepName identifies one requirement of a Cloudflare Tunnel ingress.
type TunnelStepName string

const (
	// TunnelExists is the tunnel Ozal created in the Cloudflare account.
	TunnelExists TunnelStepName = "tunnel"
	// TunnelIngress sends every hostname routed to the tunnel to this edge.
	TunnelIngress TunnelStepName = "ingress"
	// TunnelConnector is cloudflared on this host, connected to Cloudflare.
	TunnelConnector TunnelStepName = "connector"
	// TunnelLogin is the DNS record of the sign-in host (login_url), which is
	// not an application, so no application panel creates it.
	TunnelLogin TunnelStepName = "login"
)

// TunnelState is where a step stands. The values match zonesetup's so the
// console renders both checklists the same way.
type TunnelState string

const (
	TunnelDone    TunnelState = "done"
	TunnelTodo    TunnelState = "todo"
	TunnelBlocked TunnelState = "blocked"
	TunnelUnknown TunnelState = "unknown"
	TunnelWaiting TunnelState = "waiting"
)

// TunnelAction is what an apply does for a Todo step. Empty: only the
// operator can.
type TunnelAction string

const (
	CreateTunnel      TunnelAction = "create_tunnel"
	SetIngress        TunnelAction = "set_ingress"
	CreateLoginRecord TunnelAction = "create_login_record"
)

// IngressRule is one rule of a remotely managed tunnel's configuration.
type IngressRule struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
}

// DesiredIngress is a single catch-all rule to this edge. Ozal routes by
// host and refuses hosts it does not serve, and Cloudflare routes only
// records of this account to the tunnel, so no per-hostname list is needed:
// a new application needs a DNS record, never a tunnel change.
func DesiredIngress(origin string) []IngressRule {
	return []IngressRule{{Service: origin}}
}

// TunnelObserved is what the edge read locally and from the account.
type TunnelObserved struct {
	// Local is the tunnel this edge created, from its state directory; nil
	// when it has created none.
	Local *TunnelRecord
	// Read is true when the Cloudflare API answered for that tunnel; the
	// facts below are only meaningful then.
	Read bool
	// Exists is false when the account no longer has the tunnel (deleted in
	// the dashboard).
	Exists bool
	// Remote is true for a remotely managed tunnel (config_src cloudflare).
	Remote      bool
	Ingress     []IngressRule
	Status      string
	Connections int
	// TokenSaved is true when the connector token file is present.
	TokenSaved bool

	// LoginHost is the sign-in host ("" without a passkey login_url).
	// LoginRead is true when its address records were read; LoginRecords
	// are their types and contents, e.g. "CNAME x.cfargotunnel.com".
	LoginHost    string
	LoginRead    bool
	LoginRecords []string
}

// TunnelRecord is the tunnel identity kept in the state directory. It holds
// no secret: the connector token lives in its own file.
type TunnelRecord struct {
	AccountID string `json:"account_id"`
	ID        string `json:"id"`
	Name      string `json:"name"`
}

// TunnelStep is one requirement with its outcome.
type TunnelStep struct {
	Name   TunnelStepName `json:"name"`
	State  TunnelState    `json:"state"`
	Action TunnelAction   `json:"action,omitempty"`
	Title  string         `json:"title"`
	Detail string         `json:"detail"`
	// Manual is what the operator runs or changes, for a step the edge
	// cannot do itself.
	Manual string `json:"manual,omitempty"`
}

// ConnectorCommand is the one host command that starts cloudflared with the
// token Ozal saved; the unit is installed by `ozal install service`.
const ConnectorCommand = "sudo systemctl enable --now ozal-cloudflared"

// PlanTunnel returns the steps in the order an apply runs them. It never
// decides to replace what it did not create: a tunnel configured locally
// (config_src local) is reported, not reconfigured.
func PlanTunnel(o TunnelObserved, origin string) []TunnelStep {
	return []TunnelStep{tunnelStep(o), ingressStep(o, origin), loginStep(o), connectorStep(o)}
}

// TunnelReady reports whether visitors can reach this edge through the tunnel.
func TunnelReady(steps []TunnelStep) bool {
	for _, s := range steps {
		if s.State != TunnelDone {
			return false
		}
	}
	return true
}

func tunnelStep(o TunnelObserved) TunnelStep {
	s := TunnelStep{Name: TunnelExists, Title: "Tunnel in your Cloudflare account"}
	switch {
	case o.Local == nil:
		s.State, s.Action = TunnelTodo, CreateTunnel
		s.Detail = "Ozal creates a tunnel with the API token and keeps its connector token on this host."
	case !o.Read:
		s.State, s.Detail = TunnelUnknown, "Cloudflare did not answer for tunnel "+o.Local.Name+"."
	case !o.Exists:
		s.State, s.Action = TunnelTodo, CreateTunnel
		s.Detail = "Tunnel " + o.Local.Name + " no longer exists in the account. Creating it again gives it a new address: records made for the old one must be changed."
	case !o.Remote:
		s.State = TunnelBlocked
		s.Detail = "Tunnel " + o.Local.Name + " is managed by a local cloudflared configuration file; Ozal does not change it."
	case !o.TokenSaved:
		s.State, s.Action = TunnelTodo, CreateTunnel
		s.Detail = "The connector token is missing on this host; Ozal fetches it again from Cloudflare."
	default:
		s.State, s.Detail = TunnelDone, "Tunnel "+o.Local.Name+" ("+o.Local.ID+")."
	}
	return s
}

func ingressStep(o TunnelObserved, origin string) TunnelStep {
	s := TunnelStep{Name: TunnelIngress, Title: "Every address goes to this edge"}
	switch {
	case o.Local == nil || (o.Read && !o.Exists):
		s.State, s.Detail = TunnelWaiting, "Set once the tunnel exists."
	case !o.Read:
		s.State, s.Detail = TunnelUnknown, "Could not be read."
	case !o.Remote:
		s.State, s.Detail = TunnelBlocked, "Managed by the local cloudflared configuration."
	case sameIngress(o.Ingress, DesiredIngress(origin)):
		s.State, s.Detail = TunnelDone, "Cloudflare forwards to "+origin+"."
	default:
		s.State, s.Action = TunnelTodo, SetIngress
		s.Detail = "The tunnel must forward every hostname to " + origin + "; Ozal decides which ones it serves."
	}
	return s
}

func connectorStep(o TunnelObserved) TunnelStep {
	s := TunnelStep{Name: TunnelConnector, Title: "cloudflared connected from this host"}
	switch {
	case o.Local == nil || !o.Read || !o.Exists || !o.TokenSaved:
		s.State, s.Detail = TunnelWaiting, "Started once the tunnel exists."
	case o.Connections > 0 && (o.Status == "healthy" || o.Status == "degraded"):
		s.State, s.Detail = TunnelDone, "Connected ("+o.Status+")."
	default:
		s.State, s.Manual = TunnelTodo, ConnectorCommand
		s.Detail = "No cloudflared is connected. Install cloudflared on this host, then start the connector Ozal prepared."
	}
	return s
}

func loginStep(o TunnelObserved) TunnelStep {
	s := TunnelStep{Name: TunnelLogin, Title: "Sign-in address"}
	if o.LoginHost == "" {
		s.State, s.Detail = TunnelDone, "No sign-in host to publish: this edge has no passkey login_url."
		return s
	}
	s.Title = "Sign-in address " + o.LoginHost
	if o.Local == nil || (o.Read && !o.Exists) {
		s.State, s.Detail = TunnelWaiting, "Created once the tunnel exists."
		return s
	}
	want := "CNAME " + strings.ToLower(o.Local.ID) + ".cfargotunnel.com"
	switch {
	case !o.LoginRead:
		s.State, s.Detail = TunnelUnknown, "Its DNS record could not be read."
		s.Manual = "Create a proxied CNAME record " + o.LoginHost + " pointing to " + strings.ToLower(o.Local.ID) + ".cfargotunnel.com."
	case len(o.LoginRecords) == 0:
		s.State, s.Action = TunnelTodo, CreateLoginRecord
		s.Detail = "Visitors sign in at " + o.LoginHost + "; it needs a proxied CNAME to the tunnel."
	case len(o.LoginRecords) == 1 && strings.EqualFold(o.LoginRecords[0], want):
		s.State, s.Detail = TunnelDone, "Points at the tunnel."
	default:
		s.State = TunnelBlocked
		s.Detail = o.LoginHost + " already has " + strings.Join(o.LoginRecords, ", ") + ". Ozal never replaces a record it did not create."
		s.Manual = "Change " + o.LoginHost + " to a proxied CNAME pointing to " + strings.ToLower(o.Local.ID) + ".cfargotunnel.com."
	}
	return s
}

func sameIngress(current, want []IngressRule) bool {
	if len(current) != len(want) {
		return false
	}
	for i := range want {
		if !strings.EqualFold(current[i].Hostname, want[i].Hostname) || strings.TrimSuffix(current[i].Service, "/") != want[i].Service {
			return false
		}
	}
	return true
}
