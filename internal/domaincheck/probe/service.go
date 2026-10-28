package probe

import (
	"context"
	"errors"

	"github.com/octaviosantacruz/switchboard/internal/certs"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/domaincheck"
)

// ErrNoProvider means no DNS provider is configured to create records.
var ErrNoProvider = errors.New("no DNS provider is configured; add the record by hand")

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

// Check observes host as cfg serves it.
func (s *Service) Check(ctx context.Context, cfg config.Config, host string) Report {
	target, proxied := "", false
	if cfg.DNS != nil {
		target, proxied = cfg.DNS.Target, cfg.DNS.Proxied
	}
	proxies, _ := config.ParseTrustedProxies(cfg.TrustedProxies)
	observation := s.Prober.Observe(ctx, host, cfg.TLS.Enabled, proxies)
	report := Report{Host: host, Assessment: domaincheck.Assess(host, target, cfg.TLS.Enabled, observation), Addresses: []string{}}
	if record, ok := domaincheck.Instruction(host, target, proxied); ok {
		report.Record = &record
	}
	if cfg.DNS != nil && cfg.DNS.Cloudflare != nil {
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
	if cfg.DNS == nil || cfg.DNS.Cloudflare == nil {
		return domaincheck.Record{}, ErrNoProvider
	}
	record, ok := domaincheck.Instruction(host, cfg.DNS.Target, cfg.DNS.Proxied)
	if !ok {
		return domaincheck.Record{}, ErrNoProvider
	}
	token, err := config.ResolveSecret(cfg.DNS.Cloudflare.Token)
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
