package cloudflare

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/ozal-project/ozal/internal/config"
)

// LetsEncrypt is the ACME directory an enabled edge uses by default.
const LetsEncrypt = "https://acme-v02.api.letsencrypt.org/directory"

// ErrAlreadyActive says the edge already obtains certificates by DNS-01
// through Cloudflare with this token: there is nothing to change.
var ErrAlreadyActive = errors.New("cloudflare: certificates by DNS-01 are already active with this token")

// Enabled is the reviewed change that moves an edge to certificates by
// DNS-01 through Cloudflare: the host configuration to write, the zones it
// will hold certificates for, and which zone covers each address.
type Enabled struct {
	Host   config.Config
	Zones  []string
	Covers map[string]string
	// Local are addresses that need no public certificate (localhost, IPs).
	Local []string
}

// PlanEnable works out that change. host is the host configuration alone
// (what will be written), merged the configuration in effect (host plus the
// runtime's routes), cloudflareZones the zones the token can see, and target
// the public address visitors reach this edge at (dns.target), used when the
// configuration has none yet. Every
// address the edge serves over HTTPS today must be covered by a zone the
// token can reach, or the plan is refused: switching would leave it without
// a certificate.
func PlanEnable(host, merged config.Config, tokenRef, stateDir, target string, cloudflareZones []string) (Enabled, error) {
	if !merged.TLS.Enabled {
		return Enabled{}, errors.New("this edge serves plain HTTP (tls.enabled is off); certificates by DNS-01 are for an edge that terminates HTTPS itself")
	}
	if current := host.TLS.DNS01; current != nil {
		if (current.DNSProvider == "" || current.DNSProvider == "cloudflare") && current.DNSToken == tokenRef {
			return Enabled{}, ErrAlreadyActive
		}
		return Enabled{}, fmt.Errorf("this edge already obtains certificates by DNS-01 (provider %q); change that in the host configuration", current.DNSProvider)
	}
	if merged.DNS != nil && merged.DNS.Target != "" {
		target = merged.DNS.Target
	}
	if strings.TrimSpace(target) == "" {
		return Enabled{}, errors.New("the public address of this edge is needed (dns.target): the IP address or host name visitors reach it at")
	}
	if strings.TrimSpace(merged.TLS.Email) == "" {
		return Enabled{}, errors.New("tls.email is needed for the ACME account; add it to the host configuration first")
	}
	plan := Enabled{Covers: map[string]string{}}
	var unreachable []string
	for _, name := range servedNames(merged) {
		if local(name) {
			plan.Local = append(plan.Local, name)
			continue
		}
		zone, ok := zoneFor(name, cloudflareZones)
		if !ok {
			unreachable = append(unreachable, name)
			continue
		}
		plan.Covers[name] = zone
		if !slices.Contains(plan.Zones, zone) {
			plan.Zones = append(plan.Zones, zone)
		}
	}
	if len(unreachable) > 0 {
		return Enabled{}, fmt.Errorf("the token cannot reach the Cloudflare zone of %s; these addresses would lose their certificate. Give the token access to their zones, or keep HTTP-01", strings.Join(unreachable, ", "))
	}
	if len(plan.Zones) == 0 {
		return Enabled{}, errors.New("this edge serves no public address yet; add an application first, then enable certificates")
	}
	slices.Sort(plan.Zones)

	change := func(cfg config.Config) config.Config {
		next := config.Clone(cfg)
		next.TLS.Domains = slices.Clone(plan.Zones)
		next.TLS.DNS01 = &config.DNS01{StateDir: stateDir, DirectoryURL: LetsEncrypt, DNSProvider: "cloudflare", DNSToken: tokenRef}
		if next.DNS == nil {
			next.DNS = &config.DNS{}
		}
		if next.DNS.Target == "" {
			next.DNS.Target = strings.TrimSpace(target)
		}
		next.DNS.Cloudflare = &config.DNSCloudflare{Token: tokenRef}
		return next
	}
	// The configuration in effect must validate after the change, not only
	// the host file: the runtime's routes are what the zones must cover.
	if _, err := config.Validate(change(merged)); err != nil {
		return Enabled{}, err
	}
	validated, err := config.Validate(change(host))
	if err != nil {
		return Enabled{}, err
	}
	plan.Host = *validated
	return plan, nil
}

// servedNames are the addresses the edge answers over HTTPS: every route's
// host, the sign-in host, and the names the host configuration already lists.
func servedNames(cfg config.Config) []string {
	var names []string
	add := func(name string) {
		name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
		if i := strings.LastIndexByte(name, ':'); i >= 0 && !strings.HasSuffix(name, "]") {
			name = name[:i]
		}
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	for _, route := range cfg.Routes {
		add(route.Host)
	}
	if cfg.Auth.Passkeys != nil {
		if u, err := url.Parse(cfg.Auth.Passkeys.LoginURL); err == nil {
			add(u.Hostname())
		}
	}
	for _, domain := range cfg.TLS.Domains {
		add(strings.TrimPrefix(domain, "*."))
	}
	return names
}

func local(name string) bool {
	if _, err := netip.ParseAddr(strings.Trim(name, "[]")); err == nil {
		return true
	}
	return !strings.Contains(name, ".") || name == "localhost" || strings.HasSuffix(name, ".localhost")
}

// zoneFor picks the certificate zone for an address: the address itself when
// it is a Cloudflare zone's apex, otherwise its parent, which covers it and
// its siblings with one wildcard. The zone must sit inside a Cloudflare zone
// the token sees, or the challenge record cannot be written.
func zoneFor(name string, cloudflareZones []string) (string, bool) {
	zone := name
	if !slices.Contains(cloudflareZones, name) {
		_, parent, ok := strings.Cut(name, ".")
		if !ok {
			return "", false
		}
		zone = parent
	}
	if _, err := config.NormalizeZoneName(zone); err != nil {
		return "", false
	}
	for _, cf := range cloudflareZones {
		if zone == cf || strings.HasSuffix(zone, "."+cf) {
			return zone, true
		}
	}
	return "", false
}
