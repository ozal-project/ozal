package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/domaincheck"
)

// runApp implements `ozal app alias`: give an application already served by
// this edge a second hostname. It is a client of the admin API, so the route
// is validated, saved with the reviewed digest and published as an event like
// any console change; the DNS record is created by the edge with its own
// provider credential, so the operator never holds a DNS token.
func runApp(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "alias" {
		return errors.New("usage: ozal app alias -from HOST -to HOST [-apply] [-manual-dns] [-wait 90s]")
	}
	fs := flag.NewFlagSet("app alias", flag.ContinueOnError)
	fs.SetOutput(out)
	af := addAdminFlags(fs)
	from := fs.String("from", "", "hostname of the existing public application")
	to := fs.String("to", "", "new hostname that serves the same application")
	apply := fs.Bool("apply", false, "do it (default: print the plan)")
	manualDNS := fs.Bool("manual-dns", false, "save the route and check it, but leave the DNS record to the operator")
	wait := fs.Duration("wait", 90*time.Second, "how long to wait for the hostname to connect")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, err := af.client()
	if err != nil {
		return err
	}
	return aliasApplication(ctx, out, client, aliasRequest{
		From: *from, To: *to, Apply: *apply, ManualDNS: *manualDNS, Wait: *wait, Every: 5 * time.Second,
	})
}

type aliasRequest struct {
	From, To  string
	Apply     bool
	ManualDNS bool
	// Wait bounds the final connection check; Every is the pause between tries.
	Wait, Every time.Duration
}

// aliasSetup is the part of /_/api/setup the alias needs.
type aliasSetup struct {
	Digest string         `json:"config_sha256"`
	TLS    bool           `json:"tls_enabled"`
	Routes []config.Route `json:"routes"`
	DNS    struct {
		Provider string `json:"provider"`
	} `json:"dns"`
}

// connectionReport is the connection checklist for one hostname.
type connectionReport struct {
	Connected   bool                `json:"connected"`
	DNS         domaincheck.Step    `json:"dns"`
	Certificate domaincheck.Step    `json:"certificate"`
	Reach       domaincheck.Step    `json:"reach"`
	Record      *domaincheck.Record `json:"record,omitempty"`
}

func (r connectionReport) ready() bool {
	return r.Connected && r.DNS.State == "ok" && r.Certificate.State == "ok" && r.Reach.State == "ok"
}

func (r connectionReport) summary() string {
	return fmt.Sprintf("dns=%s certificate=%s reach=%s", r.DNS.State, r.Certificate.State, r.Reach.State)
}

// aliasApplication copies from's route to a new hostname. It refuses any
// source that is not a public application, and a destination that already
// has a different route, before changing anything.
func aliasApplication(ctx context.Context, out io.Writer, api *adminClient, req aliasRequest) error {
	step := func(format string, a ...any) { fmt.Fprintf(out, "==> "+format+"\n", a...) }
	note := func(format string, a ...any) { fmt.Fprintf(out, "    "+format+"\n", a...) }
	from, err := config.NormalizeHost(req.From)
	if err != nil {
		return fmt.Errorf("-from: %w", err)
	}
	to, err := config.NormalizeHost(req.To)
	if err != nil {
		return fmt.Errorf("-to: %w", err)
	}
	if from == to {
		return errors.New("-from and -to are the same hostname")
	}

	var setup aliasSetup
	if err := api.do(ctx, http.MethodGet, "/_/api/setup", nil, &setup); err != nil {
		return err
	}
	route, exists, err := aliasRoute(setup.Routes, from, to)
	if err != nil {
		return err
	}
	if setup.Digest == "" {
		return errors.New("the edge did not supply a configuration digest")
	}
	payload := map[string]any{"access_mode": "public", "route": route, "expected": setup.Digest}
	var plan struct {
		TLS string `json:"tls"`
	}
	if err := api.do(ctx, http.MethodPost, "/_/api/apps/plan", payload, &plan); err != nil {
		return err
	}
	step("plan: %s serves the same application as %s", to, from)
	note("route destination: %s; already saved: %v", route.Destination(), exists)
	note("certificate: %s; dns provider: %s", plan.TLS, orNone(setup.DNS.Provider))
	if !req.Apply {
		note("re-run with -apply to do it; %s is not changed", from)
		return nil
	}
	if !setup.TLS || (plan.TLS != "configured_coverage_certificate_not_probed" && plan.TLS != "automatic_on_first_visit") {
		return errors.New("HTTPS coverage must be configured before applying; nothing was changed")
	}
	if !req.ManualDNS && setup.DNS.Provider == "" {
		return errors.New("the edge has no DNS provider (dns.cloudflare): configure it with ozalctl edge config, or pass -manual-dns; nothing was changed")
	}

	if !exists {
		step("save the route")
		if err := api.do(ctx, http.MethodPost, "/_/api/apps", payload, &struct{}{}); err != nil {
			return err
		}
		note("saved; %s is unchanged", from)
	}
	check := "/_/api/apps/" + url.PathEscape(to) + "/check"
	step("check %s", to)
	var report connectionReport
	if err := api.do(ctx, http.MethodPost, check, nil, &report); err != nil {
		return err
	}
	note("%s", report.summary())
	if report.ready() {
		step("%s is connected; verify its public content before changing canonical URLs", to)
		return nil
	}
	if req.ManualDNS {
		return manualDNSHint(to, report)
	}
	if report.DNS.State != "ok" {
		step("create the DNS record")
		err := api.do(ctx, http.MethodPost, "/_/api/apps/"+url.PathEscape(to)+"/dns", nil, &struct{}{})
		switch {
		case err == nil:
			note("the edge confirmed the record")
		case isStatus(err, http.StatusConflict):
			// A record already exists and is never replaced: wait for it to
			// match instead of treating it as a failure.
			note("%v", err)
		default:
			return fmt.Errorf("DNS was not created (the route stays saved; nothing else was changed): %w", err)
		}
	}
	deadline := time.Now().Add(req.Wait)
	for {
		if err := api.do(ctx, http.MethodPost, check, nil, &report); err != nil {
			return err
		}
		note("%s", report.summary())
		if report.ready() {
			step("%s is connected; verify its public content before changing canonical URLs", to)
			return nil
		}
		if !time.Now().Add(req.Every).Before(deadline) {
			return fmt.Errorf("%s is not connected yet (%s); the route and record are kept, re-run to check again", to, report.summary())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(req.Every):
		}
	}
}

// aliasRoute returns the route to save for to, copied from from's. exists is
// true when to already has exactly that route.
func aliasRoute(routes []config.Route, from, to string) (route config.Route, exists bool, err error) {
	var source *config.Route
	var current *config.Route
	for i := range routes {
		switch routes[i].Host {
		case from:
			source = &routes[i]
		case to:
			current = &routes[i]
		}
	}
	if source == nil || source.RequiresAuth() || source.IsRedirect() {
		return route, false, errors.New("the source must be an existing public application; nothing was changed")
	}
	if source.Site == "" && source.Root == "" && source.Target == "" && len(source.Upstreams) == 0 && source.Connector == "" {
		return route, false, errors.New("the source route has no serving destination; nothing was changed")
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return route, false, err
	}
	if err := json.Unmarshal(encoded, &route); err != nil {
		return route, false, err
	}
	route.Host = to
	if current != nil {
		have, _ := json.Marshal(current)
		want, _ := json.Marshal(route)
		if !bytes.Equal(have, want) {
			return route, false, errors.New("the destination already has a different route; it was left unchanged")
		}
		exists = true
	}
	return route, exists, nil
}

func manualDNSHint(host string, report connectionReport) error {
	msg := fmt.Sprintf("route saved; DNS was not modified and %s is not connected yet (%s)", host, report.summary())
	if r := report.Record; r != nil {
		msg += fmt.Sprintf("; add %s %s -> %s (proxied=%v) at your DNS provider", r.Type, r.Name, r.Content, r.Proxied)
	}
	return errors.New(msg)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
