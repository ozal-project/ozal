// Package cftunnel creates and checks the Cloudflare Tunnel that publishes
// this edge without a public address (ingress.mode "cloudflare_tunnel").
//
// cloudflare.PlanTunnel is the pure core; this package is its shell: it reads
// the tunnel identity kept in the state directory and the account through
// the Cloudflare API, and carries out the steps the plan allows. It never
// runs cloudflared and never shows the connector token: the token is written
// to a file the ozal-cloudflared unit loads as a systemd credential.
package cftunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/cloudflare"
	"github.com/ozal-project/ozal/internal/config"
)

const (
	recordFile = "tunnel.json"
	// TokenFile is the connector token, read by the ozal-cloudflared unit.
	TokenFile = "connector.token"
)

// Client is the slice of the Cloudflare API the tunnel uses.
type Client interface {
	AccountForZone(ctx context.Context, name string) (string, error)
	FindTunnel(ctx context.Context, account, name string) (certs.CloudflareTunnel, bool, error)
	CreateTunnel(ctx context.Context, account, name string) (certs.CloudflareTunnel, string, error)
	TunnelToken(ctx context.Context, account, id string) (string, error)
	Tunnel(ctx context.Context, account, id string) (certs.CloudflareTunnel, error)
	TunnelIngress(ctx context.Context, account, id string) ([]certs.TunnelIngressRule, error)
	SetTunnelIngress(ctx context.Context, account, id string, rules []certs.TunnelIngressRule) error
}

var _ Client = (*certs.CloudflareClient)(nil)

// Service inspects and applies the tunnel. One apply runs at a time.
type Service struct {
	// Cloudflare builds a client from the resolved token; tests replace it.
	Cloudflare func(token string) (Client, error)
	mu         sync.Mutex
}

// New wires the real Cloudflare API.
func New() *Service {
	return &Service{Cloudflare: func(token string) (Client, error) {
		return certs.NewCloudflareClient(certs.CloudflareConfig{Token: token})
	}}
}

// Report is the tunnel as the console shows it. It never holds a token.
type Report struct {
	Mode   string                  `json:"mode"`
	Name   string                  `json:"name,omitempty"`
	ID     string                  `json:"id,omitempty"`
	Target string                  `json:"target,omitempty"`
	Origin string                  `json:"origin,omitempty"`
	Ready  bool                    `json:"ready"`
	Steps  []cloudflare.TunnelStep `json:"steps,omitempty"`
	// Error says why Cloudflare could not be read; the steps then show what
	// is known locally.
	Error   string   `json:"error,omitempty"`
	Applied []string `json:"applied,omitempty"`
}

const permissions = "the token needs Account · Cloudflare Tunnel · Edit, and Zone · DNS · Edit on the served zones"

// Target is the hostname visitor records point at (a proxied CNAME), or ""
// before the tunnel exists. It reads only local state.
func Target(cfg config.Config) string {
	if cfg.IngressMode() != config.IngressCloudflareTunnel {
		return ""
	}
	record, err := readRecord(cfg.Ingress.CloudflareTunnel.StateDir)
	if err != nil || record == nil {
		return ""
	}
	return config.CloudflareTunnelTarget(record.ID)
}

// Inspect reads the tunnel and plans what is left. It changes nothing.
func (s *Service) Inspect(ctx context.Context, cfg config.Config) Report {
	report, _, _ := s.inspect(ctx, cfg)
	return report
}

func (s *Service) inspect(ctx context.Context, cfg config.Config) (Report, Client, cloudflare.TunnelObserved) {
	report := Report{Mode: cfg.IngressMode()}
	var observed cloudflare.TunnelObserved
	if report.Mode != config.IngressCloudflareTunnel {
		return report, nil, observed
	}
	in := cfg.Ingress.CloudflareTunnel
	report.Name, report.Origin = in.Name, config.CloudflareTunnelOrigin(cfg.Listen)
	record, err := readRecord(in.StateDir)
	if err != nil {
		report.Error = err.Error()
	}
	observed.Local = record
	observed.TokenSaved = tokenSaved(in.StateDir)
	client, err := s.client(cfg)
	if err != nil {
		report.Error = err.Error()
	}
	if record != nil {
		report.ID, report.Target = record.ID, config.CloudflareTunnelTarget(record.ID)
		if client != nil {
			if err := observe(ctx, client, record, &observed); err != nil {
				report.Error = "Cloudflare did not answer for tunnel " + record.Name + " (" + permissions + "): " + err.Error()
			}
		}
	}
	report.Steps = cloudflare.PlanTunnel(observed, report.Origin)
	report.Ready = cloudflare.TunnelReady(report.Steps)
	return report, client, observed
}

func observe(ctx context.Context, client Client, record *cloudflare.TunnelRecord, o *cloudflare.TunnelObserved) error {
	tunnel, err := client.Tunnel(ctx, record.AccountID, record.ID)
	if errors.Is(err, certs.ErrTunnelMissing) {
		o.Read, o.Exists = true, false
		return nil
	}
	if err != nil {
		return err
	}
	o.Exists, o.Remote, o.Status, o.Connections = true, tunnel.Remote, tunnel.Status, tunnel.Connections
	if tunnel.Remote {
		rules, err := client.TunnelIngress(ctx, record.AccountID, record.ID)
		if err != nil && !errors.Is(err, certs.ErrTunnelMissing) {
			return err
		}
		for _, r := range rules {
			o.Ingress = append(o.Ingress, cloudflare.IngressRule{Hostname: r.Hostname, Service: r.Service})
		}
	}
	o.Read = true
	return nil
}

func (s *Service) client(cfg config.Config) (Client, error) {
	token, err := config.ResolveSecret(cfg.Ingress.CloudflareTunnel.Token)
	if err != nil {
		return nil, errors.New("the Cloudflare token (ingress.cloudflare_tunnel.token) could not be read")
	}
	client, err := s.Cloudflare(token)
	if err != nil {
		return nil, errors.New("the Cloudflare client is misconfigured")
	}
	return client, nil
}

// Apply creates or recovers the tunnel, saves its connector token and sets
// its ingress to this edge. Starting cloudflared stays with the operator.
func (s *Service) Apply(ctx context.Context, cfg config.Config) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	report, client, observed := s.inspect(ctx, cfg)
	if report.Mode != config.IngressCloudflareTunnel {
		return report, errors.New("this edge is not configured for a Cloudflare Tunnel (ingress.mode)")
	}
	if client == nil {
		return report, errors.New(report.Error)
	}
	var applied []string
	finish := func(err error) (Report, error) {
		after := s.Inspect(ctx, cfg)
		after.Applied = applied
		return after, err
	}
	if report.Steps[0].Action == cloudflare.CreateTunnel {
		record, err := s.ensureTunnel(ctx, client, cfg, observed)
		if err != nil {
			return finish(err)
		}
		observed.Local = record
		applied = append(applied, string(cloudflare.CreateTunnel))
	} else if report.Steps[0].State != cloudflare.TunnelDone {
		return finish(errors.New(report.Steps[0].Detail))
	}
	// The ingress follows the tunnel: one created just now has none yet.
	want := cloudflare.DesiredIngress(config.CloudflareTunnelOrigin(cfg.Listen))
	current, err := client.TunnelIngress(ctx, observed.Local.AccountID, observed.Local.ID)
	if err != nil && !errors.Is(err, certs.ErrTunnelMissing) {
		return finish(failed("reading the tunnel's routing", err))
	}
	if !sameRules(current, want) {
		rules := make([]certs.TunnelIngressRule, len(want))
		for i, r := range want {
			rules[i] = certs.TunnelIngressRule{Hostname: r.Hostname, Service: r.Service}
		}
		if err := client.SetTunnelIngress(ctx, observed.Local.AccountID, observed.Local.ID, rules); err != nil {
			return finish(failed("pointing the tunnel at this edge", err))
		}
		applied = append(applied, string(cloudflare.SetIngress))
	}
	return finish(nil)
}

// ensureTunnel reuses the tunnel this edge recorded when it still exists,
// adopts an active remotely managed tunnel with the configured name (a lost
// state, or a create whose answer was lost), or creates one. In every case it
// saves the identity and the connector token.
func (s *Service) ensureTunnel(ctx context.Context, client Client, cfg config.Config, o cloudflare.TunnelObserved) (*cloudflare.TunnelRecord, error) {
	in := cfg.Ingress.CloudflareTunnel
	var record cloudflare.TunnelRecord
	var token string
	if o.Local != nil && o.Read && o.Exists {
		record = *o.Local
	} else {
		account := ""
		if o.Local != nil {
			account = o.Local.AccountID
		} else {
			domain := accountDomain(cfg)
			if domain == "" {
				return nil, errors.New("set auth.passkeys.rp_id or auth.cookie_domain to your domain so Ozal can find its Cloudflare account")
			}
			var err error
			if account, err = client.AccountForZone(ctx, domain); err != nil {
				return nil, failed("finding the Cloudflare account of "+domain, err)
			}
		}
		existing, found, err := client.FindTunnel(ctx, account, in.Name)
		if err != nil {
			return nil, failed("looking for tunnel "+in.Name, err)
		}
		switch {
		case found && !existing.Remote:
			return nil, fmt.Errorf("tunnel %s exists but is managed by a local cloudflared configuration; choose another ingress.cloudflare_tunnel.name", in.Name)
		case found:
			record = cloudflare.TunnelRecord{AccountID: account, ID: existing.ID, Name: existing.Name}
		default:
			created, createdToken, err := client.CreateTunnel(ctx, account, in.Name)
			if errors.Is(err, certs.ErrAmbiguous) {
				// The answer was lost; the tunnel may exist. Never make a second.
				if again, ok, findErr := client.FindTunnel(ctx, account, in.Name); findErr == nil && ok {
					created, createdToken, err = again, "", nil
				}
			}
			if err != nil {
				return nil, failed("creating tunnel "+in.Name, err)
			}
			record = cloudflare.TunnelRecord{AccountID: account, ID: created.ID, Name: created.Name}
			token = createdToken
		}
	}
	if token == "" {
		var err error
		if token, err = client.TunnelToken(ctx, record.AccountID, record.ID); err != nil {
			return nil, failed("fetching the connector token", err)
		}
	}
	if err := save(in.StateDir, record, token); err != nil {
		return nil, err
	}
	return &record, nil
}

func accountDomain(cfg config.Config) string {
	if cfg.Auth.Passkeys != nil && cfg.Auth.Passkeys.RPID != "" && cfg.Auth.Passkeys.RPID != "localhost" {
		return cfg.Auth.Passkeys.RPID
	}
	return strings.TrimPrefix(cfg.Auth.CookieDomain, ".")
}

func failed(what string, err error) error {
	switch {
	case errors.Is(err, certs.ErrAmbiguous):
		return errors.New(what + ": Cloudflare did not confirm the change; check again before retrying")
	case errors.Is(err, certs.ErrTunnelMissing):
		return errors.New(what + ": the tunnel no longer exists")
	default:
		// The API's own words, plus what the token must allow: a wrong or
		// under-scoped token is the usual cause.
		return errors.New(what + " failed: " + err.Error() + ". Check the API token: " + permissions + ".")
	}
}

func sameRules(current []certs.TunnelIngressRule, want []cloudflare.IngressRule) bool {
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

func readRecord(dir string) (*cloudflare.TunnelRecord, error) {
	data, err := os.ReadFile(filepath.Join(dir, recordFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the tunnel state in %s could not be read", dir)
	}
	var record cloudflare.TunnelRecord
	if err := json.Unmarshal(data, &record); err != nil || record.ID == "" || record.AccountID == "" {
		return nil, fmt.Errorf("the tunnel state in %s is damaged", dir)
	}
	return &record, nil
}

func tokenSaved(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, TokenFile))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// save writes the token first and the identity last, each atomically and
// readable only by this service's account. A crash in between leaves no
// identity, so the next apply finds the tunnel by name and saves both again.
func save(dir string, record cloudflare.TunnelRecord, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("Cloudflare returned an empty connector token")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("the tunnel state directory %s could not be created", dir)
	}
	if err := writeFile(dir, TokenFile, []byte(token)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(dir, recordFile, append(data, '\n'))
}

func writeFile(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return fmt.Errorf("%s could not be written in %s", name, dir)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("%s could not be protected", name)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("%s could not be written in %s", name, dir)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("%s could not be written in %s", name, dir)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%s could not be written in %s", name, dir)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("%s could not be saved in %s", name, dir)
	}
	return nil
}
