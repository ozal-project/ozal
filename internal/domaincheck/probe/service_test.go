package probe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
)

type recorder struct{ created []certs.SiteRecord }

func (r *recorder) CreateSiteRecord(_ context.Context, record certs.SiteRecord) (certs.SiteRecord, error) {
	r.created = append(r.created, record)
	record.ID = "rec-1"
	return record, nil
}

func tunnelCfg(t *testing.T) (config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	token := filepath.Join(dir, "api.token")
	if err := os.WriteFile(token, []byte("api-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	return config.Config{Listen: "127.0.0.1:8888", Ingress: &config.Ingress{Mode: config.IngressCloudflareTunnel,
		CloudflareTunnel: &config.CloudflareTunnelIngress{Token: "file:" + token, StateDir: state, Name: "ozal"}}}, state
}

func TestRecordsPointAtTheTunnel(t *testing.T) {
	cfg, state := tunnelCfg(t)
	created := &recorder{}
	s := &Service{Cloudflare: func(token string) (RecordCreator, error) {
		if token != "api-token" {
			return nil, errors.New("wrong token")
		}
		return created, nil
	}}
	if _, err := s.CreateRecord(t.Context(), cfg, "notes.example.com"); !errors.Is(err, ErrNoTunnel) {
		t.Fatalf("created a record before the tunnel existed: %v", err)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "tunnel.json"), []byte(`{"account_id":"acc","id":"C0FFEE00-1","name":"ozal"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	records := RecordsFor(cfg)
	if !records.Tunnel || !records.Proxied || records.Target != "c0ffee00-1.cfargotunnel.com" || records.Field != "ingress.cloudflare_tunnel.token" {
		t.Fatalf("records = %+v", records)
	}
	record, err := s.CreateRecord(t.Context(), cfg, "notes.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if record.Type != "CNAME" || record.Content != "c0ffee00-1.cfargotunnel.com" || !record.Proxied || len(created.created) != 1 {
		t.Fatalf("record = %+v, created %+v", record, created.created)
	}
}

func TestDirectRecordsAreUnchanged(t *testing.T) {
	cfg := config.Config{DNS: &config.DNS{Target: "203.0.113.10", Proxied: true, Cloudflare: &config.DNSCloudflare{Token: "env:CF"}}}
	records := RecordsFor(cfg)
	if records.Tunnel || records.Target != "203.0.113.10" || !records.Proxied || records.Field != "dns.cloudflare.token" {
		t.Fatalf("records = %+v", records)
	}
	if got := RecordsFor(config.Config{}); got != (Records{}) {
		t.Fatalf("no dns = %+v", got)
	}
}
