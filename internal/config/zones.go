package config

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Zone is a domain the edge serves with its own certificate, the apex and
// one level of subdomains (EDGE-008). Zones added through the admin API
// live in the runtime file; the bases of tls.domains are zones too, owned by
// the host configuration.
type Zone struct {
	Name string `json:"name"`
}

// maxZones bounds the runtime list: each zone is one certificate renewed
// in turn, a few seconds of issuance each.
const maxZones = 100

// Zone sources, as the console shows them.
const (
	ZoneFromConfig  = "config"
	ZoneFromRuntime = "runtime"
)

// ServedZone is one zone the edge serves and where it is defined.
type ServedZone struct {
	Name   string
	Source string
}

// NormalizeZoneName lowercases a domain, drops a trailing dot and refuses
// what can never hold a public certificate: wildcards, ports, IP literals,
// single labels and .localhost names.
func NormalizeZoneName(raw string) (string, error) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	host, err := NormalizeHost(name)
	if err != nil || host != name || strings.ContainsAny(name, "*:") {
		return "", fmt.Errorf("zone %q is not a plain domain such as example.com", raw)
	}
	if !strings.Contains(name, ".") || name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return "", fmt.Errorf("zone %q is not a public domain", raw)
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return "", fmt.Errorf("zone %q is an IP address, not a domain", raw)
	}
	last := name[strings.LastIndexByte(name, '.')+1:]
	if strings.Trim(last, "0123456789") == "" {
		return "", fmt.Errorf("zone %q is not a public domain", raw)
	}
	return name, nil
}

// configZones are the zones the host configuration defines: the base of
// every tls.domains entry, in order, once each.
func configZones(tls TLS) []string {
	var out []string
	for _, domain := range tls.Domains {
		base := strings.TrimPrefix(domain, "*.")
		if name, err := NormalizeZoneName(base); err == nil && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// ServedZones lists every zone the edge serves: the host configuration's
// first, then the runtime's. Empty unless the edge owns its certificates
// (tls.dns01).
func ServedZones(cfg Config) []ServedZone {
	if !cfg.TLS.Enabled || cfg.TLS.DNS01 == nil {
		return nil
	}
	var out []ServedZone
	seen := map[string]bool{}
	for _, name := range configZones(cfg.TLS) {
		seen[name] = true
		out = append(out, ServedZone{Name: name, Source: ZoneFromConfig})
	}
	for _, zone := range cfg.Zones {
		if !seen[zone.Name] {
			seen[zone.Name] = true
			out = append(out, ServedZone{Name: zone.Name, Source: ZoneFromRuntime})
		}
	}
	return out
}

// ServedZoneNames is ServedZones without the sources.
func ServedZoneNames(cfg Config) []string {
	zones := ServedZones(cfg)
	names := make([]string, len(zones))
	for i, zone := range zones {
		names[i] = zone.Name
	}
	return names
}

// ZoneChallenge is the DNS record a zone needs for its certificate: the
// operator creates Name as a CNAME to Target when Delegated, so only the
// zone holding Target needs a provider. Without a delegation the TXT is
// written at Name itself by the configured provider.
type ZoneChallenge struct {
	Name      string
	Target    string
	Delegated bool
}

// ChallengeFor returns where a zone's DNS-01 challenge is written. An
// explicit tls.dns01.challenge_delegation wins; with the builtin DNS server
// a zone without one is delegated to "<zone>.<builtin zone>", the only
// place that server can write.
func ChallengeFor(tls TLS, zone string) ZoneChallenge {
	name := "_acme-challenge." + zone
	if tls.DNS01 == nil {
		return ZoneChallenge{Name: name, Target: name}
	}
	if target, ok := tls.DNS01.ChallengeDelegation[zone]; ok {
		return ZoneChallenge{Name: name, Target: target, Delegated: true}
	}
	if tls.DNS01.DNSProvider == "builtin" && tls.DNS01.Builtin != nil {
		return ZoneChallenge{Name: name, Target: zone + "." + tls.DNS01.Builtin.Zone, Delegated: true}
	}
	return ZoneChallenge{Name: name, Target: name}
}

// normalizeZones validates runtime zones once TLS is normalized: unique
// public domains, an edge that owns its certificates, and a challenge the
// configured provider can write.
func normalizeZones(cfg *Config) error {
	if len(cfg.Zones) == 0 {
		cfg.Zones = nil
		return nil
	}
	if !cfg.TLS.Enabled || cfg.TLS.DNS01 == nil {
		return fmt.Errorf("config: zones require tls.dns01: the edge must issue their certificates itself")
	}
	if len(cfg.Zones) > maxZones {
		return fmt.Errorf("config: at most %d zones, got %d", maxZones, len(cfg.Zones))
	}
	seen := map[string]bool{}
	for i, zone := range cfg.Zones {
		name, err := NormalizeZoneName(zone.Name)
		if err != nil {
			return fmt.Errorf("config: zones[%d]: %w", i, err)
		}
		if seen[name] {
			return fmt.Errorf("config: zone %q is listed twice", name)
		}
		seen[name] = true
		cfg.Zones[i].Name = name
		if err := zoneProviderCanWrite(cfg.TLS, name); err != nil {
			return err
		}
	}
	return nil
}

// zoneProviderCanWrite refuses a zone whose challenge lies outside the zone
// a single-zone provider (Hetzner, RFC 2136) manages: it would fail at the
// first issuance.
func zoneProviderCanWrite(tls TLS, zone string) error {
	home := strings.TrimPrefix(ChallengeFor(tls, zone).Target, "_acme-challenge.")
	var provider, providerZone string
	switch {
	case tls.DNS01.DNSProvider == "hetzner" && tls.DNS01.Hetzner != nil:
		provider, providerZone = "hetzner", tls.DNS01.Hetzner.Zone
	case tls.DNS01.DNSProvider == "rfc2136" && tls.DNS01.RFC2136 != nil:
		provider, providerZone = "rfc2136", tls.DNS01.RFC2136.Zone
	default:
		return nil
	}
	if home != providerZone && !strings.HasSuffix(home, "."+providerZone) {
		return fmt.Errorf("config: zone %q: its challenge (%s) is outside the %s zone %q; add a challenge_delegation for it", zone, home, provider, providerZone)
	}
	return nil
}

// WithZone adds a runtime zone. A zone the edge already serves is refused,
// so a repeated request never looks like a change.
func WithZone(cfg Config, raw string) (Config, error) {
	name, err := NormalizeZoneName(raw)
	if err != nil {
		return Config{}, err
	}
	if slices.Contains(ServedZoneNames(cfg), name) {
		return Config{}, fmt.Errorf("zone %q is already served", name)
	}
	next := Clone(cfg)
	next.Zones = append(next.Zones, Zone{Name: name})
	validated, err := Validate(next)
	if err != nil {
		return Config{}, err
	}
	return *validated, nil
}

// WithoutZone removes a runtime zone. A zone the host configuration defines
// is changed there, and a zone that still serves a route's only certificate
// is refused: removing it would take that application offline.
func WithoutZone(cfg Config, raw string) (Config, error) {
	name, err := NormalizeZoneName(raw)
	if err != nil {
		return Config{}, err
	}
	index := slices.IndexFunc(cfg.Zones, func(z Zone) bool { return z.Name == name })
	if index < 0 {
		if slices.Contains(configZones(cfg.TLS), name) {
			return Config{}, fmt.Errorf("zone %q comes from tls.domains in the host configuration; change it there", name)
		}
		return Config{}, fmt.Errorf("zone %q is not served", name)
	}
	next := Clone(cfg)
	next.Zones = slices.Delete(next.Zones, index, index+1)
	if hosts := ZoneHosts(cfg, name); len(hosts) > 0 {
		remaining := ServedZoneNames(next)
		for _, host := range hosts {
			if !zonesCover(remaining, host) {
				return Config{}, fmt.Errorf("zone %q still serves %s; remove or move those applications first", name, strings.Join(hosts, ", "))
			}
		}
	}
	validated, err := Validate(next)
	if err != nil {
		return Config{}, err
	}
	return *validated, nil
}

// ZoneHosts lists the route hostnames a zone's certificate covers (the
// apex and one level below), without ports.
func ZoneHosts(cfg Config, zone string) []string {
	var out []string
	for _, route := range cfg.Routes {
		host := routeHostname(route.Host)
		if zoneCovers(zone, host) && !slices.Contains(out, host) {
			out = append(out, host)
		}
	}
	return out
}

func routeHostname(host string) string {
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.HasSuffix(host, "]") {
		return host[:i]
	}
	return host
}

// zoneCovers mirrors the certificate rule: the apex, or exactly one more label.
func zoneCovers(zone, host string) bool {
	if host == zone {
		return true
	}
	label, ok := strings.CutSuffix(host, "."+zone)
	return ok && label != "" && !strings.Contains(label, ".")
}

func zonesCover(zones []string, host string) bool {
	return slices.ContainsFunc(zones, func(zone string) bool { return zoneCovers(zone, host) })
}
