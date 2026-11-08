package probe

import (
	"context"
	"errors"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/cftunnel"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/domaincheck"
)

// ErrNoProvider means no DNS provider is configured to create records.
var ErrNoProvider = errors.New("no DNS provider is configured; add the record by hand")

// ErrNoTunnel means records cannot point at the Cloudflare Tunnel yet.
var ErrNoTunnel = errors.New("create the Cloudflare Tunnel under Domains first; records point at it")

// RecordCreator creates one address record without replacing any.
type RecordCreator interface {
	CreateSiteRecord(ctx context.Context, record certs.SiteRecord) (certs.SiteRecord, error)
}

// Service answers the console's connection questions for a hostname.
type Service struct {
	Prober *Prober
	// Cloudflare builds a client from the resolved token; tests replace it.
	Cloudflare func(token string) (RecordCreator, error)
}

// NewService probes with tokens and creates records through Cloudflare's API.
func NewService(tokens *Tokens) *Service {
	return &Service{Prober: NewProber(tokens), Cloudflare: func(token string) (RecordCreator, error) {
		return certs.NewCloudflareClient(certs.CloudflareConfig{Token: token})
	}}
}

// Report is the connection checklist with the record the hostname needs.
type Report struct {
	Host string `json:"host"`
	domaincheck.Assessment
	Record    *domaincheck.Record `json:"record,omitempty"`
	Provider  string              `json:"provider,omitempty"`
	Addresses []string            `json:"addresses"`
}

// Records is where visitor records point and which token may write them.
type Records struct {
	Target  string
	Proxied bool
	// Token is a secret reference ("" without a provider); Field names it.
	Token, Field string
	// Tunnel is true when records point at a Cloudflare Tunnel.
	Tunnel bool
}

// RecordsFor resolves dns and ingress into one answer. With a Cloudflare
// Tunnel every record is a proxied CNAME to the tunnel Ozal created (none
// before it exists), written with the tunnel's token when dns.cloudflare is
// absent.
func RecordsFor(cfg config.Config) Records {
	var r Records
	if cfg.DNS != nil {
		r.Target, r.Proxied = cfg.DNS.Target, cfg.DNS.Proxied
		if cfg.DNS.Cloudflare != nil {
			r.Token, r.Field = cfg.DNS.Cloudflare.Token, "dns.cloudflare.token"
		}
	}
	if cfg.IngressMode() == config.IngressCloudflareTunnel {
		r.Tunnel, r.Target, r.Proxied = true, cftunnel.Target(cfg), true
		if r.Token == "" {
			r.Token, r.Field = cfg.Ingress.CloudflareTunnel.Token, "ingress.cloudflare_tunnel.token"
		}
	}
	return r
}

// Check observes host as cfg serves it.
func (s *Service) Check(ctx context.Context, cfg config.Config, host string) Report {
	records := RecordsFor(cfg)
	target, proxied := records.Target, records.Proxied
	proxies, _ := config.ParseTrustedProxies(cfg.TrustedProxies)
	// Behind a tunnel visitors use Cloudflare's HTTPS; probing http:// would
	// only meet Always Use HTTPS's redirect.
	https := cfg.TLS.Enabled || records.Tunnel
	observation := s.Prober.Observe(ctx, host, https, proxies)
	assessment := domaincheck.Assess(host, target, https, observation)
	if records.Tunnel {
		assessment = domaincheck.AssessTunnel(assessment, target, observation)
	}
	report := Report{Host: host, Assessment: assessment, Addresses: []string{}}
	if record, ok := domaincheck.Instruction(host, target, proxied); ok {
		report.Record = &record
	}
	if records.Token != "" {
		report.Provider = "cloudflare"
	}
	for _, a := range observation.Addresses {
		report.Addresses = append(report.Addresses, a.String())
	}
	return report
}

// CreateRecord adds host's record through the configured provider. The token
// is resolved here, used for this call and dropped.
func (s *Service) CreateRecord(ctx context.Context, cfg config.Config, host string) (domaincheck.Record, error) {
	records := RecordsFor(cfg)
	if records.Token == "" {
		return domaincheck.Record{}, ErrNoProvider
	}
	if records.Tunnel && records.Target == "" {
		return domaincheck.Record{}, ErrNoTunnel
	}
	record, ok := domaincheck.Instruction(host, records.Target, records.Proxied)
	if !ok {
		return domaincheck.Record{}, ErrNoProvider
	}
	token, err := config.ResolveSecret(records.Token)
	if err != nil {
		return domaincheck.Record{}, errors.New("the DNS provider token could not be read")
	}
	client, err := s.Cloudflare(token)
	if err != nil {
		return domaincheck.Record{}, errors.New("the DNS provider is misconfigured")
	}
	if _, err := client.CreateSiteRecord(ctx, certs.SiteRecord{Type: record.Type, Name: record.Name, Content: record.Content, Proxied: record.Proxied}); err != nil {
		return domaincheck.Record{}, err
	}
	return record, nil
}
