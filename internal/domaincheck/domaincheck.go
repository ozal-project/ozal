// Package domaincheck decides whether a hostname is connected to this edge
// and what the operator should do next (UI-006). It is the pure core: the
// adapter in probe observes DNS and a public request; Assess turns that
// observation into three steps with a stable state and a sentence each.
package domaincheck

import (
	"net/netip"
	"strings"
)

// Record is one visitor-facing DNS record.
type Record struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

// Reach is what a public request for the hostname found.
type Reach string

const (
	// ReachEdge: the request came back with this edge's one-time token.
	ReachEdge Reach = "this_edge"
	// ReachOther: some server answered, but not this edge (old hosting).
	ReachOther Reach = "other_server"
	// ReachCertificate: an address answered with a certificate that does
	// not verify for the hostname (yet).
	ReachCertificate Reach = "certificate"
	// ReachProxyOrigin: the DNS provider's proxy answered that it could not
	// reach or was refused by the origin (Cloudflare 52x).
	ReachProxyOrigin Reach = "proxy_origin_error"
	// ReachNone: nothing answered.
	ReachNone Reach = "unreachable"
)

// Lookup is the outcome of resolving the hostname.
type Lookup string

const (
	LookupFound    Lookup = "found"
	LookupNotFound Lookup = "not_found"
	LookupFailed   Lookup = "failed"
)

// Observation is what the adapter saw. ViaProxy is true when every address
// belongs to a trusted proxy (the provider hides the origin behind it).
type Observation struct {
	Lookup    Lookup
	Addresses []netip.Addr
	CNAME     string
	ViaProxy  bool
	Reach     Reach
	Status    int
}

// States of a step.
const (
	StateOK   = "ok"
	StateWait = "wait"
	StateBad  = "bad"
)

// Step is one line of the connection checklist.
type Step struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// Assessment is the checklist. Connected is true only when a public request
// reached this edge.
type Assessment struct {
	Connected   bool `json:"connected"`
	DNS         Step `json:"dns"`
	Certificate Step `json:"certificate"`
	Reach       Step `json:"reach"`
}

// Instruction returns the one record that makes host reach target: A for an
// IPv4 address, AAAA for IPv6, CNAME for a hostname. A port in host is
// dropped (DNS has none). ok is false when no target is configured.
func Instruction(host, target string, proxied bool) (Record, bool) {
	if target == "" {
		return Record{}, false
	}
	name := Hostname(host)
	record := Record{Name: name, Content: target, Proxied: proxied, Type: "CNAME"}
	if addr, err := netip.ParseAddr(target); err == nil {
		record.Type = "A"
		if addr.Is6() && !addr.Is4In6() {
			record.Type = "AAAA"
		}
	}
	return record, true
}

// Hostname drops a port from a route host.
func Hostname(host string) string {
	if strings.HasPrefix(host, "[") {
		if end := strings.IndexByte(host, ']'); end > 0 {
			return host[1:end]
		}
	}
	if i := strings.LastIndexByte(host, ':'); i >= 0 && strings.Count(host, ":") == 1 {
		return host[:i]
	}
	return host
}

// Assess turns an observation into the checklist. https is false when this
// edge serves plain HTTP and TLS is someone else's concern.
func Assess(host, target string, https bool, o Observation) Assessment {
	a := Assessment{Connected: o.Reach == ReachEdge}
	name := Hostname(host)

	switch {
	case o.Lookup == LookupFailed:
		a.DNS = Step{StateWait, "The DNS lookup failed. Check again in a moment."}
	case o.Lookup == LookupNotFound || len(o.Addresses) == 0:
		a.DNS = Step{StateBad, "No DNS record for " + name + " yet."}
	case target != "" && (pointsAt(o, target)):
		a.DNS = Step{StateOK, "Points to " + target + "."}
	case o.ViaProxy:
		a.DNS = Step{StateOK, "Answers through your DNS provider's proxy, which hides the origin; the request below confirms where it goes."}
	case target == "" || a.Connected:
		a.DNS = Step{StateOK, "Resolves to " + joinAddrs(o.Addresses) + "."}
	default:
		a.DNS = Step{StateBad, "Points to " + joinAddrs(o.Addresses) + ", not " + target + ". Change it to the record shown."}
	}

	switch o.Reach {
	case ReachEdge:
		a.Reach = Step{StateOK, "A public request for " + name + " reached this edge."}
	case ReachOther:
		a.Reach = Step{StateBad, "Another server answered: the hostname still points at the previous hosting or another service."}
	case ReachCertificate:
		a.Reach = Step{StateWait, "An address answered, but its certificate is not valid for " + name + " yet."}
	case ReachProxyOrigin:
		a.Reach = Step{StateBad, "Your DNS provider's proxy could not reach this edge. With Authenticated Origin Pulls, turn it on for the whole zone; check the proxy's SSL mode."}
	default:
		if a.DNS.State == StateOK {
			a.Reach = Step{StateBad, "Nothing answered at the public address. Check that this edge accepts connections from the internet."}
		} else {
			a.Reach = Step{StateWait, "Waiting for DNS."}
		}
	}

	switch {
	case !https:
		a.Certificate = Step{StateOK, "Not checked: this edge serves plain HTTP, so HTTPS is handled in front of it."}
	case o.Reach == ReachEdge || o.Reach == ReachOther:
		a.Certificate = Step{StateOK, "The public address has a valid certificate."}
	case o.Reach == ReachCertificate:
		a.Certificate = Step{StateWait, "A first certificate is requested on the first visit and takes up to a minute. Check again."}
	default:
		a.Certificate = Step{StateWait, "Checked once the hostname reaches this edge."}
	}
	return a
}

func pointsAt(o Observation, target string) bool {
	if strings.EqualFold(strings.TrimSuffix(o.CNAME, "."), target) {
		return true
	}
	addr, err := netip.ParseAddr(target)
	if err != nil {
		return false
	}
	for _, a := range o.Addresses {
		if a.Unmap() == addr.Unmap() {
			return true
		}
	}
	return false
}

func joinAddrs(addrs []netip.Addr) string {
	parts := make([]string, 0, len(addrs))
	for i, a := range addrs {
		if i == 3 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}
