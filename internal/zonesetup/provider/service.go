// Package provider observes a zone at its DNS provider and proxy and applies
// the steps zonesetup plans (EDGE-009). Cloudflare is the one provider with an
// API here; without it the same steps are read from public DNS where possible
// and otherwise described for the operator to do by hand.
package provider

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/originauth"
	"github.com/ozal-project/ozal/internal/zonesetup"
)

// Client is the slice of the Cloudflare API zone setup uses.
type Client interface {
	RecordsAt(ctx context.Context, name string) ([]certs.NameRecord, error)
	CreateChallengeCNAME(ctx context.Context, name, target string) error
	ZoneSSLMode(ctx context.Context, zone string) (string, error)
	SetZoneSSLStrict(ctx context.Context, zone string) error
	ZoneOriginPull(ctx context.Context, zone string) (bool, []certs.ZoneClientCertificate, error)
	UploadZoneClientCertificate(ctx context.Context, zone string, certPEM, keyPEM []byte) (certs.ZoneClientCertificate, error)
	EnableZoneOriginPull(ctx context.Context, zone string) error
}

// Identity is the edge's own proxy certificate (originauth.Identity).
type Identity interface {
	Ensure(now time.Time) (certPEM, keyPEM []byte, err error)
}

// Service answers and applies zone setup.
type Service struct {
	// Cloudflare builds a client from the resolved token; tests replace it.
	Cloudflare func(token string) (Client, error)
	// LookupCNAME reads the challenge record when no provider API is set.
	LookupCNAME func(ctx context.Context, host string) (string, error)
	// Identity and Trust exist when the public listener checks client
	// certificates (tls.origin_client_ca_file); nil otherwise.
	Identity Identity
	Trust    *originauth.Trust
	Now      func() time.Time
}

// New wires the real Cloudflare API and resolver.
func New(identity Identity, trust *originauth.Trust) *Service {
	return &Service{
		Cloudflare: func(token string) (Client, error) {
			return certs.NewCloudflareClient(certs.CloudflareConfig{Token: token})
		},
		LookupCNAME: net.DefaultResolver.LookupCNAME,
		Identity:    identity,
		Trust:       trust,
		Now:         time.Now,
	}
}

// Report is a zone's setup as the console shows it.
type Report struct {
	Zone     string           `json:"zone"`
	Provider string           `json:"provider,omitempty"`
	Ready    bool             `json:"ready"`
	Steps    []zonesetup.Step `json:"steps"`
	// Error says why the provider could not be read; the steps then fall
	// back to what public DNS shows and manual instructions.
	Error   string   `json:"error,omitempty"`
	Applied []string `json:"applied,omitempty"`
}

const permissions = "the token needs Zone · DNS · Edit, Zone · Zone Settings · Edit and Zone · SSL and Certificates · Edit on this zone"

// Inspect observes zone and plans what is left. It changes nothing.
func (s *Service) Inspect(ctx context.Context, cfg config.Config, zone string) Report {
	report, _ := s.inspect(ctx, cfg, zone)
	return report
}

func (s *Service) inspect(ctx context.Context, cfg config.Config, zone string) (Report, Client) {
	challenge := config.ChallengeFor(cfg.TLS, zone)
	observed := zonesetup.Observed{
		Zone:               zone,
		Proxied:            cfg.DNS != nil && cfg.DNS.Proxied,
		ChallengeDelegated: challenge.Delegated,
		ChallengeName:      challenge.Name,
		ChallengeTarget:    strings.TrimSuffix(strings.ToLower(challenge.Target), "."),
		OriginAuth:         cfg.TLS.OriginClientCAFile != "" && s.Trust != nil,
	}
	report := Report{Zone: zone}
	client, err := s.client(cfg)
	switch {
	case err != nil:
		report.Error = err.Error()
	case client != nil:
		report.Provider = "cloudflare"
		if err := s.observeProvider(ctx, client, &observed); err != nil {
			report.Error = "Cloudflare did not answer for " + zone + " (" + permissions + "): " + err.Error()
			client = nil
		}
	}
	if !observed.Provider && observed.ChallengeDelegated {
		s.observeDNS(ctx, &observed)
	}
	report.Steps = zonesetup.Plan(observed)
	report.Ready = zonesetup.Ready(report.Steps)
	return report, client
}

// client resolves the provider token for this call only. No provider is not
// an error: the steps are then manual.
func (s *Service) client(cfg config.Config) (Client, error) {
	if cfg.DNS == nil || cfg.DNS.Cloudflare == nil {
		return nil, nil
	}
	token, err := config.ResolveSecret(cfg.DNS.Cloudflare.Token)
	if err != nil {
		return nil, errors.New("the Cloudflare token (dns.cloudflare.token) could not be read")
	}
	client, err := s.Cloudflare(token)
	if err != nil {
		return nil, errors.New("the Cloudflare provider is misconfigured")
	}
	return client, nil
}

func (s *Service) observeProvider(ctx context.Context, client Client, o *zonesetup.Observed) error {
	if o.ChallengeDelegated {
		records, err := client.RecordsAt(ctx, o.ChallengeName)
		if err != nil {
			return err
		}
		o.ChallengeRead = true
		for _, r := range records {
			if r.Type == "CNAME" {
				o.ChallengeCurrent = r.Content
			} else if o.ChallengeCurrent == "" {
				o.ChallengeCurrent = "a " + r.Type + " record"
			}
		}
	}
	mode, err := client.ZoneSSLMode(ctx, o.Zone)
	if err != nil {
		return err
	}
	o.SSLMode = mode
	if o.OriginAuth {
		enabled, list, err := client.ZoneOriginPull(ctx, o.Zone)
		if err != nil {
			return err
		}
		o.PullEnabled = enabled
		o.PullTrusted = s.latestTrusted(list)
	}
	o.Provider = true
	return nil
}

// latestTrusted reports whether the most recently uploaded certificate the
// proxy may present passes this edge's policy.
func (s *Service) latestTrusted(list []certs.ZoneClientCertificate) bool {
	var latest *certs.ZoneClientCertificate
	for i := range list {
		c := &list[i]
		if c.Status != "active" && c.Status != "pending_deployment" {
			continue
		}
		if latest == nil || c.UploadedOn > latest.UploadedOn {
			latest = c
		}
	}
	if latest == nil {
		return false
	}
	chain := originauth.ParseCertificates([]byte(latest.Certificate))
	return len(chain) > 0 && s.Trust.Trusts(chain, s.now())
}

func (s *Service) observeDNS(ctx context.Context, o *zonesetup.Observed) {
	lookup := s.LookupCNAME
	if lookup == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	canonical, err := lookup(ctx, o.ChallengeName)
	var dnsErr *net.DNSError
	switch {
	case err == nil:
		o.ChallengeRead = true
		current := strings.TrimSuffix(strings.ToLower(canonical), ".")
		if current != strings.ToLower(o.ChallengeName) { // no CNAME answers with the name itself
			o.ChallengeCurrent = current
		}
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		o.ChallengeRead = true
	}
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Apply does every step the provider API can do, in order, and reports the
// zone as it stands afterwards. It stops at the first failure; what was
// done before it stays done and is listed in Applied.
func (s *Service) Apply(ctx context.Context, cfg config.Config, zone string) (Report, error) {
	report, client := s.inspect(ctx, cfg, zone)
	if client == nil {
		if report.Error != "" {
			return report, errors.New(report.Error)
		}
		return report, errors.New("no DNS provider API is configured (dns.cloudflare); do the steps by hand")
	}
	var applied []string
	for _, step := range report.Steps {
		if step.State != zonesetup.Todo || step.Action == "" {
			continue
		}
		if err := s.do(ctx, client, zone, cfg, step.Action); err != nil {
			after := s.Inspect(ctx, cfg, zone)
			after.Applied = applied
			return after, err
		}
		applied = append(applied, string(step.Action))
	}
	after := s.Inspect(ctx, cfg, zone)
	after.Applied = applied
	return after, nil
}

func (s *Service) do(ctx context.Context, client Client, zone string, cfg config.Config, action zonesetup.Action) error {
	switch action {
	case zonesetup.CreateCNAME:
		challenge := config.ChallengeFor(cfg.TLS, zone)
		if err := client.CreateChallengeCNAME(ctx, challenge.Name, challenge.Target); err != nil {
			return failed("the DNS record for certificates", err)
		}
	case zonesetup.SetStrict:
		if err := client.SetZoneSSLStrict(ctx, zone); err != nil {
			return failed("Full (strict)", err)
		}
	case zonesetup.UploadIdentity:
		if s.Identity == nil {
			return errors.New("this edge has no origin identity to upload")
		}
		certPEM, keyPEM, err := s.Identity.Ensure(s.now())
		if err != nil {
			return errors.New("the edge could not create its proxy certificate")
		}
		if _, err := client.UploadZoneClientCertificate(ctx, zone, certPEM, keyPEM); err != nil {
			return failed("the proxy certificate upload", err)
		}
		if err := client.EnableZoneOriginPull(ctx, zone); err != nil {
			return failed("turning on Authenticated Origin Pulls", err)
		}
	case zonesetup.EnablePull:
		if err := client.EnableZoneOriginPull(ctx, zone); err != nil {
			return failed("turning on Authenticated Origin Pulls", err)
		}
	default:
		return errors.New("unknown setup action")
	}
	return nil
}

func failed(what string, err error) error {
	switch {
	case errors.Is(err, certs.ErrNotEmpty):
		return errors.New(what + ": a record is already there; it was left unchanged")
	case errors.Is(err, certs.ErrAmbiguous):
		return errors.New(what + ": Cloudflare did not confirm the change; check the zone before retrying")
	default:
		return errors.New(what + " failed: " + err.Error())
	}
}

// compile-time check: the real client satisfies Client.
var _ Client = (*certs.CloudflareClient)(nil)
