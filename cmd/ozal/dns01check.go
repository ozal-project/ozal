package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

// checkLevel orders what a check found. Only a failure makes the command exit
// non-zero: a warning is something the operator must read, not a refusal.
type checkLevel string

const (
	checkOK   checkLevel = "ok"
	checkWarn checkLevel = "warn"
	checkFail checkLevel = "FAIL"
)

type checkFinding struct {
	Level checkLevel
	Text  string
}

// checkEnv is everything the preflight needs from the outside world, so tests
// never touch public DNS or bind a port.
type checkEnv struct {
	LookupCNAME func(ctx context.Context, host string) (string, error)
	LookupHost  func(ctx context.Context, host string) ([]string, error)
	// Bind tries to listen on addr for the network ("udp" or "tcp") and
	// releases it at once.
	Bind func(network, addr string) error
}

func systemCheckEnv(resolverAddr string) checkEnv {
	resolver := net.DefaultResolver
	if resolverAddr != "" {
		resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, resolverAddr)
		}}
	}
	return checkEnv{
		LookupCNAME: resolver.LookupCNAME,
		LookupHost:  resolver.LookupHost,
		Bind: func(network, addr string) error {
			if network == "udp" {
				c, err := net.ListenPacket("udp", addr)
				if err != nil {
					return err
				}
				return c.Close()
			}
			l, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			return l.Close()
		},
	}
}

// checkDNS01 verifies, before any issuance, the things that so far only showed
// up as a failed validation at the CA: the CNAME at each domain, the glue of
// the built-in nameservers and the port the built-in server needs. It cannot
// prove the port is reachable from the Internet; it says so.
func checkDNS01(ctx context.Context, cfg *config.Config, env checkEnv) []checkFinding {
	var out []checkFinding
	add := func(level checkLevel, format string, args ...any) {
		out = append(out, checkFinding{level, fmt.Sprintf(format, args...)})
	}
	dns01 := cfg.TLS.DNS01
	if dns01 == nil || !cfg.TLS.Enabled {
		return []checkFinding{{checkFail, "tls.dns01 is not configured: nothing to check"}}
	}

	bases := make([]string, 0, len(dns01.ChallengeDelegation))
	for base := range dns01.ChallengeDelegation {
		bases = append(bases, base)
	}
	sort.Strings(bases)
	for _, base := range bases {
		target := dns01.ChallengeDelegation[base]
		name := "_acme-challenge." + base
		canonical, err := env.LookupCNAME(ctx, name)
		canonical = strings.TrimSuffix(strings.ToLower(canonical), ".")
		switch {
		case err != nil || canonical == name:
			add(checkFail, "%s has no CNAME; create it at the DNS that serves %s: %s CNAME %s.", name, base, name, target)
		case canonical != target:
			add(checkFail, "%s points at %s, expected %s", name, canonical, target)
		default:
			add(checkOK, "%s -> %s", name, target)
		}
	}
	if len(bases) == 0 {
		add(checkOK, "no challenge_delegation: the provider writes the TXT at each domain itself")
	}

	if dns01.DNSProvider != "builtin" || dns01.Builtin == nil {
		return out
	}
	b := dns01.Builtin
	host, _, err := net.SplitHostPort(b.Listen)
	if err != nil {
		add(checkFail, "builtin.listen %q is not host:port", b.Listen)
		return out
	}
	for _, ns := range b.Nameservers {
		addrs, err := env.LookupHost(ctx, ns)
		if err != nil || len(addrs) == 0 {
			add(checkFail, "nameserver %s does not resolve; the parent zone needs its glue (an A record at the registrar or parent DNS) pointing at %s", ns, host)
			continue
		}
		if !containsIP(addrs, host) {
			add(checkFail, "nameserver %s resolves to %s but builtin.listen is %s", ns, strings.Join(addrs, ","), host)
			continue
		}
		add(checkOK, "nameserver %s -> %s", ns, host)
	}
	for _, network := range []string{"udp", "tcp"} {
		if err := env.Bind(network, b.Listen); err != nil {
			add(checkWarn, "%s %s cannot be bound here (%v); expected if the edge is already running, otherwise grant CAP_NET_BIND_SERVICE or free the port", network, b.Listen, err)
			continue
		}
		add(checkOK, "%s %s can be bound", network, b.Listen)
	}
	add(checkWarn, "reachability of %s from the Internet (host firewall, cloud firewall) cannot be checked from this host; test it from outside with: dig @%s %s SOA", b.Listen, host, b.Zone)
	return out
}

func containsIP(addrs []string, want string) bool {
	wantIP := net.ParseIP(want)
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && wantIP != nil && ip.Equal(wantIP) {
			return true
		}
	}
	return false
}

func runDNS01Check(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("dns01 check", flag.ContinueOnError)
	fs.SetOutput(out)
	path := fs.String("config", "config.json", "path to routing config")
	resolver := fs.String("resolver", "", "DNS server host:port to ask instead of the system resolver (e.g. 1.1.1.1:53, to avoid a cached negative answer)")
	timeout := fs.Duration("timeout", 30*time.Second, "overall limit for the lookups")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	return reportDNS01Check(out, checkDNS01(ctx, cfg, systemCheckEnv(*resolver)))
}

func reportDNS01Check(out io.Writer, findings []checkFinding) error {
	failed := 0
	for _, f := range findings {
		fmt.Fprintf(out, "dns01 check: %-4s %s\n", f.Level, f.Text)
		if f.Level == checkFail {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("dns01 check: %d problem(s) to fix before issuing", failed)
	}
	fmt.Fprintln(out, "dns01 check: no blocking problem found (warnings are not proof of failure)")
	return nil
}
