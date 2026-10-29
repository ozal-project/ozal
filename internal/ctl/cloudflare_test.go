package ctl

import (
	"context"
	"encoding/json"
	cf "github.com/ozal-project/ozal/internal/cloudflare"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type fakeCloudflare struct {
	current, reference cf.OriginPull
	set                cf.Ruleset
	writes, reads      int
	race               bool
}

func (f *fakeCloudflare) OriginPull(_ context.Context, host string) (cf.OriginPull, error) {
	if host == f.reference.Hostname {
		return f.reference, nil
	}
	return f.current, nil
}
func (f *fakeCloudflare) SetOriginPull(_ context.Context, p cf.OriginPull) error {
	f.writes++
	f.current = p
	return nil
}
func (f *fakeCloudflare) ConfigurationRules(context.Context, string) (cf.Ruleset, error) {
	f.reads++
	if f.race && f.reads == 2 {
		f.set.Rules[1].Enabled = false
	}
	return f.set, nil
}
func (f *fakeCloudflare) SaveConfigurationRule(_ context.Context, set cf.Ruleset, p cf.RulePlan) error {
	f.writes++
	for i, r := range f.set.Rules {
		if r.ID == p.Before.ID {
			f.set.Rules[i] = p.After
		}
	}
	return nil
}
func TestCloudflarePlanBackupScopedWriteAndConcurrentRefusal(t *testing.T) {
	rule, _ := cf.DeployRule("deploy.example.com")
	rule.ID = "managed"
	rule.Parameters = map[string]json.RawMessage{"bic": json.RawMessage(`true`)}
	unrelated := cf.Rule{ID: "unrelated", Expression: "true"}
	for _, race := range []bool{false, true} {
		f := &fakeCloudflare{set: cf.Ruleset{ZoneID: "zone", ID: "set", Rules: []cf.Rule{unrelated, rule}}, race: race}
		backup := filepath.Join(t.TempDir(), "before.json")
		r := CloudflareRequest{Kind: "deploy-rule", Host: "deploy.example.com", Backup: backup}
		if err := Cloudflare(context.Background(), io.Discard, f, r); err != nil {
			t.Fatal(err)
		}
		if f.writes != 0 {
			t.Fatal("plan wrote provider")
		}
		if _, err := os.Stat(backup); !os.IsNotExist(err) {
			t.Fatal("plan wrote backup")
		}
		f.reads = 0
		r.Apply = true
		err := Cloudflare(context.Background(), io.Discard, f, r)
		if (err != nil) != race {
			t.Fatal(err)
		}
		if race && f.writes != 0 {
			t.Fatal("overwrote concurrent change")
		}
		if !race && (f.writes != 1 || f.set.Rules[0].ID != "unrelated") {
			t.Fatal("unrelated rule changed")
		}
		info, err := os.Stat(backup)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(info, err)
		}
	}
	f := &fakeCloudflare{current: cf.OriginPull{ZoneID: "zone", Hostname: "new.example.com"}, reference: cf.OriginPull{ZoneID: "zone", Hostname: "old.example.com", CertificateID: "cert", Enabled: true, Exists: true}}
	if err := Cloudflare(context.Background(), io.Discard, f, CloudflareRequest{Kind: "origin-pull", Host: f.current.Hostname, Like: f.reference.Hostname, Backup: filepath.Join(t.TempDir(), "origin.json"), Apply: true}); err != nil || f.writes != 1 {
		t.Fatal(err, f.writes)
	}
}
