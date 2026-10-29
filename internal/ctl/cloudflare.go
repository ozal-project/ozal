package ctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	cf "github.com/ozal-project/ozal/internal/cloudflare"
)

type CloudflareOperations interface {
	OriginPull(context.Context, string) (cf.OriginPull, error)
	SetOriginPull(context.Context, cf.OriginPull) error
	ConfigurationRules(context.Context, string) (cf.Ruleset, error)
	SaveConfigurationRule(context.Context, cf.Ruleset, cf.RulePlan) error
}
type CloudflareRequest struct {
	Kind, Host, Like, Backup string
	Apply                    bool
}

func metadataBackup(path string, value any) error {
	if path == "" {
		return errors.New("a new private metadata backup is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("cannot prepare private metadata backup")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("metadata backup must be new; nothing changed")
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(value); err != nil {
		return errors.New("metadata backup failed; nothing changed")
	}
	if f.Sync() != nil {
		return errors.New("metadata backup sync failed; nothing changed")
	}
	return nil
}

func Cloudflare(ctx context.Context, out io.Writer, provider CloudflareOperations, r CloudflareRequest) error {
	host, err := cf.Hostname(r.Host)
	if err != nil {
		return err
	}
	switch r.Kind {
	case "origin-pull":
		like, err := cf.Hostname(r.Like)
		if err != nil {
			return errors.New("an explicit reference hostname is required")
		}
		current, err := provider.OriginPull(ctx, host)
		if err != nil {
			return err
		}
		reference, err := provider.OriginPull(ctx, like)
		if err != nil {
			return err
		}
		want, changed, err := cf.PlanOriginPull(host, current, reference)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Origin Pulls %s: change=%t apply=%t (same certificate as %s).\n", host, changed, r.Apply, like)
		if !r.Apply || !changed {
			return nil
		}
		if err := metadataBackup(r.Backup, map[string]any{"version": 1, "kind": r.Kind, "hostname": host, "before": current, "after": want}); err != nil {
			return err
		}
		fresh, err := provider.OriginPull(ctx, host)
		if err != nil || fresh != current {
			return errors.New("origin pulls changed during review; nothing written")
		}
		freshReference, err := provider.OriginPull(ctx, like)
		if err != nil || freshReference != reference {
			return errors.New("reference Origin Pulls changed; nothing written")
		}
		if err := provider.SetOriginPull(ctx, want); err != nil {
			return err
		}
		actual, err := provider.OriginPull(ctx, host)
		if err != nil || actual != want {
			return errors.New("origin pulls update is not confirmed; metadata backup retained")
		}
	case "deploy-rule":
		current, err := provider.ConfigurationRules(ctx, host)
		if err != nil {
			return err
		}
		plan, err := cf.PlanDeployRule(host, current)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "CI configuration rule %s: %s apply=%t.\n", host, plan.Action, r.Apply)
		if !r.Apply || plan.Action == "ok" {
			return nil
		}
		if err := metadataBackup(r.Backup, map[string]any{"version": 1, "kind": r.Kind, "hostname": host, "zone_id": current.ZoneID, "ruleset_id": current.ID, "before": plan.Before, "after": plan.After}); err != nil {
			return err
		}
		fresh, err := provider.ConfigurationRules(ctx, host)
		if err != nil {
			return err
		}
		again, err := cf.PlanDeployRule(host, fresh)
		if err != nil || again.Action != plan.Action || fresh.ID != current.ID || !reflect.DeepEqual(again.Before, plan.Before) {
			return errors.New("CI rule changed during review; nothing written")
		}
		if err := provider.SaveConfigurationRule(ctx, fresh, again); err != nil {
			return err
		}
		actual, err := provider.ConfigurationRules(ctx, host)
		if err != nil {
			return errors.New("CI rule update was not confirmed; metadata backup retained")
		}
		confirmed, err := cf.PlanDeployRule(host, actual)
		if err != nil || confirmed.Action != "ok" {
			return errors.New("CI rule update was not confirmed; metadata backup retained")
		}
	default:
		return errors.New("cloudflare operation must be origin-pull or deploy-rule")
	}
	fmt.Fprintln(out, "Cloudflare read-back confirmed the hostname operation; metadata backup retained.")
	return nil
}
