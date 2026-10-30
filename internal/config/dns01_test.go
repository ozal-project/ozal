package config

import (
	"strings"
	"testing"
)

func dns01Config(t *testing.T, tlsJSON string) *Config {
	t.Helper()
	path := writeConfig(t, `{
		"listen": ":8888",
		"tls": `+tlsJSON+`,
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestDNS01ConfigAcceptsTheBoundedScope(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{
		"enabled": true,
		"domains": ["Preview.Example.COM"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/ozal/certs/",
			"directory_url": "https://acme-staging-v02.api.letsencrypt.org/directory",
			"certificate_roots_file": "/etc/ozal/./staging-roots.crt",
			"dns_token": "env:CLOUDFLARE_DNS_TOKEN"
		}
	}`)
	if cfg.TLS.DNS01 == nil {
		t.Fatal("tls.dns01 was dropped")
	}
	if cfg.TLS.Domains[0] != "preview.example.com" {
		t.Fatalf("domain = %q, want normalized", cfg.TLS.Domains[0])
	}
	if cfg.TLS.DNS01.StateDir != "/var/lib/ozal/certs" {
		t.Fatalf("state dir = %q, want cleaned absolute path", cfg.TLS.DNS01.StateDir)
	}
	if cfg.TLS.DNS01.DNSToken != "env:CLOUDFLARE_DNS_TOKEN" {
		t.Fatalf("token reference = %q", cfg.TLS.DNS01.DNSToken)
	}
	if cfg.TLS.DNS01.CertificateRootsFile != "/etc/ozal/staging-roots.crt" {
		t.Fatal("certificate roots path was not normalized")
	}
	// Clone must not share the pointer.
	clone := Clone(*cfg)
	clone.TLS.DNS01.StateDir = "/elsewhere"
	if cfg.TLS.DNS01.StateDir == "/elsewhere" {
		t.Fatal("Clone shares the dns01 pointer")
	}
}

func TestDNS01ConfigRefusals(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	valid := func() string {
		return `{
			"enabled": true,
			"domains": ["preview.example.com"],
			"email": "ops@example.com",
			"dns01": {
				"state_dir": "/var/lib/ozal/certs",
				"directory_url": "https://acme.example/directory",
				"dns_token": "env:TOKEN"
			}
		}`
	}
	tests := []struct {
		name   string
		mutate func(*TLS)
		want   string
	}{
		{"disabled tls", func(tls *TLS) { tls.Enabled = false }, "requires tls.enabled"},
		{"no domain", func(tls *TLS) { tls.Domains = nil }, "at least one domain"},
		{"nine domains", func(tls *TLS) {
			tls.Domains = []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com", "e.example.com", "f.example.com",
				"g.example.com", "h.example.com", "i.example.com"}
		}, "1 to 8 domains"},
		{"repeated domain", func(tls *TLS) { tls.Domains = append(tls.Domains, "PREVIEW.example.com") }, "twice"},
		{"name the wildcard covers", func(tls *TLS) { tls.Domains = []string{"*.example.com", "preview.example.com"} }, "already covered"},
		{"bad second name", func(tls *TLS) { tls.Domains = append(tls.Domains, "*.com") }, "tls.domains[1]"},
		{"missing state dir", func(tls *TLS) { tls.DNS01.StateDir = "" }, "requires state_dir"},
		{"relative state dir", func(tls *TLS) { tls.DNS01.StateDir = "certs" }, "absolute path"},
		{"relative certificate roots", func(tls *TLS) { tls.DNS01.CertificateRootsFile = "roots.crt" }, "absolute path"},
		{"missing email", func(tls *TLS) { tls.Email = "" }, "requires tls.email"},
		{"missing directory", func(tls *TLS) { tls.DNS01.DirectoryURL = "" }, "requires directory_url"},
		{"http directory", func(tls *TLS) { tls.DNS01.DirectoryURL = "http://acme.example/directory" }, "https URL"},
		{"missing token", func(tls *TLS) { tls.DNS01.DNSToken = " " }, "requires dns_token"},
		{"empty file reference", func(tls *TLS) { tls.DNS01.DNSToken = "file: " }, "names no file"},
		{"hostname with port", func(tls *TLS) { tls.Domains[0] = "preview.example.com:443" }, "plain hostname"},
		{"wildcard over a public suffix", func(tls *TLS) { tls.Domains[0] = "*.com" }, "registered domain"},
		{"wildcard not at the left", func(tls *TLS) { tls.Domains[0] = "a.*.example.com" }, "plain hostname"},
		{"wildcard without a dot", func(tls *TLS) { tls.Domains[0] = "*example.com" }, "plain hostname"},
		{"double wildcard", func(tls *TLS) { tls.Domains[0] = "*.*.example.com" }, "plain hostname"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, `{
				"listen": ":8888",
				"tls": `+valid()+`,
				"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
			}`)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("baseline config must load: %v", err)
			}
			tt.mutate(&cfg.TLS)
			if _, err := Validate(*cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestDNS01ConfigDoesNotRequireAutocertCache(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{
		"enabled": true,
		"domains": ["preview.example.com"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/ozal/certs",
			"directory_url": "https://acme.example/directory",
			"dns_token": "file:/etc/ozal/dns-token"
		}
	}`)
	if cfg.TLS.CacheDir != "" {
		t.Fatalf("cache_dir = %q, want it unused in dns-01 mode", cfg.TLS.CacheDir)
	}
}

func TestDNS01ConfigAcceptsASingleLevelWildcard(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{
		"enabled": true,
		"domains": ["*.Example.COM"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/ozal/certs",
			"directory_url": "https://acme.example/directory",
			"dns_token": "env:TOKEN"
		}
	}`)
	if got := cfg.TLS.Domains[0]; got != "*.example.com" {
		t.Fatalf("domain = %q, want the wildcard normalized", got)
	}
}

// A wildcard does not cover its base, so the apex rides beside it as a second
// name on the same certificate; the wildcard stays first, its identity.
func TestDNS01ConfigAcceptsAWildcardAndItsApex(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{
		"enabled": true,
		"domains": ["*.example.com", "Example.com"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/ozal/certs",
			"directory_url": "https://acme.example/directory",
			"dns_token": "env:TOKEN"
		}
	}`)
	if got, want := strings.Join(cfg.TLS.Domains, ","), "*.example.com,example.com"; got != want {
		t.Fatalf("domains = %q, want %q", got, want)
	}
}

// autocert cannot answer a wildcard: only the DNS-01 manager can, so a
// wildcard without tls.dns01 must fail at load time, not at the first handshake.
func TestWildcardRequiresDNS01(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"tls": {"enabled": true, "domains": ["*.example.com"], "email": "ops@example.com", "cache_dir": "/var/lib/ozal/acme"},
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("Load error = %v, want it to refuse a wildcard without tls.dns01", err)
	}
}

func TestSitesTokenMustDifferFromTheAdminToken(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"admin": {"token": "env:SB_ADMIN", "sites_token": "env:SB_ADMIN"},
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "sites_token") {
		t.Fatalf("Load error = %v, want sites_token refused", err)
	}
}

func rfc2136TLS(extra string) string {
	return `{
		"enabled": true,
		"domains": ["preview.example.com"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/ozal/certs",
			"directory_url": "https://acme-v02.api.letsencrypt.org/directory",
			"dns_provider": "rfc2136",
			"rfc2136": {
				"server": "ns1.example.com:53",
				"zone": "Example.COM.",
				"key_name": "ozal-key",
				"secret": "file:/etc/ozal/tsig.secret"` + extra + `
			}
		}
	}`
}

func TestDNS01ConfigAcceptsRFC2136(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, rfc2136TLS(`, "algorithm": "HMAC-SHA512"`))
	r := cfg.TLS.DNS01.RFC2136
	if cfg.TLS.DNS01.DNSProvider != "rfc2136" || r == nil {
		t.Fatalf("dns01 = %+v", cfg.TLS.DNS01)
	}
	if r.Zone != "example.com" || r.Algorithm != "hmac-sha512" || r.Server != "ns1.example.com:53" {
		t.Fatalf("rfc2136 = %+v, want normalized zone and algorithm", r)
	}
	clone := Clone(*cfg)
	clone.TLS.DNS01.RFC2136.Zone = "elsewhere.test"
	if cfg.TLS.DNS01.RFC2136.Zone != "example.com" {
		t.Fatal("Clone shares the rfc2136 pointer")
	}
}

func TestDNS01ProviderDefaultsToCloudflareAndKeepsItsRules(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{
		"enabled": true, "domains": ["preview.example.com"], "email": "ops@example.com",
		"dns01": {"state_dir": "/var/lib/ozal/certs", "directory_url": "https://acme-v02.api.letsencrypt.org/directory", "dns_token": "env:T"}
	}`)
	if cfg.TLS.DNS01.DNSProvider != "cloudflare" {
		t.Fatalf("provider = %q, want the historical default", cfg.TLS.DNS01.DNSProvider)
	}
}

func TestDNS01ProviderRefusals(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	load := func(tlsJSON string) error {
		path := writeConfig(t, `{"listen": ":8888", "tls": `+tlsJSON+`, "routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]}`)
		_, err := Load(path)
		return err
	}
	cases := map[string]struct{ tls, want string }{
		"unknown provider": {strings.Replace(rfc2136TLS(""), `"rfc2136",`, `"route53",`, 1), "dns_provider"},
		"rfc2136 block missing": {`{"enabled": true, "domains": ["preview.example.com"], "email": "o@example.com",
			"dns01": {"state_dir": "/var/lib/ozal/certs", "directory_url": "https://acme-v02.api.letsencrypt.org/directory", "dns_provider": "rfc2136"}}`, "rfc2136"},
		"cloudflare with a rfc2136 block": {strings.Replace(rfc2136TLS(""), `"dns_provider": "rfc2136",`, `"dns_token": "env:T",`, 1), "rfc2136"},
		"rfc2136 with a cloudflare token": {strings.Replace(rfc2136TLS(""), `"dns_provider": "rfc2136",`, `"dns_provider": "rfc2136", "dns_token": "env:T",`, 1), "dns_token"},
		"server without port":             {strings.Replace(rfc2136TLS(""), "ns1.example.com:53", "ns1.example.com", 1), "server"},
		"inline secret":                   {strings.Replace(rfc2136TLS(""), "file:/etc/ozal/tsig.secret", "c2VjcmV0c2VjcmV0c2VjcmV0", 1), "secret"},
		"missing key name":                {strings.Replace(rfc2136TLS(""), `"key_name": "ozal-key",`, "", 1), "key_name"},
		"weak algorithm":                  {rfc2136TLS(`, "algorithm": "hmac-md5"`), "algorithm"},
		"zone does not cover the domain":  {strings.Replace(rfc2136TLS(""), `"Example.COM."`, `"other.test"`, 1), "zone"},
		"suffix is not containment":       {strings.Replace(rfc2136TLS(""), `"Example.COM."`, `"ample.com"`, 1), "zone"},
	}
	for name, tc := range cases {
		err := load(tc.tls)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want mention of %q", name, err, tc.want)
		}
	}
}

func hetznerTLS(extra string) string {
	return `{
		"enabled": true,
		"domains": ["*.example.com", "example.com"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/ozal/certs",
			"directory_url": "https://acme-v02.api.letsencrypt.org/directory",
			"dns_provider": "Hetzner",
			"hetzner": {"zone": "Example.COM.", "token": "file:/etc/ozal/hetzner.token"` + extra + `}
		}
	}`
}

func TestDNS01ConfigAcceptsHetzner(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, hetznerTLS(""))
	h := cfg.TLS.DNS01.Hetzner
	if cfg.TLS.DNS01.DNSProvider != "hetzner" || h == nil || h.Zone != "example.com" {
		t.Fatalf("dns01 = %+v", cfg.TLS.DNS01)
	}
	clone := Clone(*cfg)
	clone.TLS.DNS01.Hetzner.Zone = "elsewhere.test"
	if cfg.TLS.DNS01.Hetzner.Zone != "example.com" {
		t.Fatal("Clone shares the hetzner pointer")
	}
}

func TestDNS01HetznerAndProviderExclusivity(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	load := func(tlsJSON string) error {
		path := writeConfig(t, `{"listen": ":8888", "tls": `+tlsJSON+`, "routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]}`)
		_, err := Load(path)
		return err
	}
	cases := map[string]struct{ tls, want string }{
		"hetzner block missing": {strings.Replace(hetznerTLS(""), `"dns_provider": "Hetzner",
			"hetzner": {"zone": "Example.COM.", "token": "file:/etc/ozal/hetzner.token"}`, `"dns_provider": "Hetzner"`, 1), "hetzner"},
		"inline token":            {strings.Replace(hetznerTLS(""), "file:/etc/ozal/hetzner.token", "abcdef", 1), "token"},
		"no zone":                 {strings.Replace(hetznerTLS(""), `"zone": "Example.COM.",`, "", 1), "zone"},
		"zone does not cover":     {strings.Replace(hetznerTLS(""), `"Example.COM."`, `"other.test"`, 1), "zone"},
		"with a cloudflare token": {strings.Replace(hetznerTLS(""), `"dns_provider": "Hetzner",`, `"dns_provider": "hetzner", "dns_token": "env:T",`, 1), "dns_token"},
		"cloudflare with hetzner": {strings.Replace(hetznerTLS(""), `"dns_provider": "Hetzner",`, `"dns_token": "env:T",`, 1), "hetzner"},
		"rfc2136 with hetzner":    {strings.Replace(rfc2136TLS(""), `"rfc2136": {`, `"hetzner": {"zone": "example.com", "token": "env:T"}, "rfc2136": {`, 1), "hetzner"},
		"hetzner with rfc2136":    {strings.Replace(hetznerTLS(""), `"hetzner": {`, `"rfc2136": {"server": "a:53", "zone": "example.com", "key_name": "k", "secret": "env:S"}, "hetzner": {`, 1), "rfc2136"},
	}
	for name, tc := range cases {
		err := load(tc.tls)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want mention of %q", name, err, tc.want)
		}
	}
}

func delegatedTLS(provider, extra string) string {
	return `{
		"enabled": true,
		"domains": ["*.example.com", "example.com"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/ozal/certs",
			"directory_url": "https://acme-v02.api.letsencrypt.org/directory",
			` + provider + `,
			"challenge_delegation": {"Example.COM": "example-com.acme.delegated.net."` + extra + `}
		}
	}`
}

const hetznerForDelegated = `"dns_provider": "hetzner", "hetzner": {"zone": "delegated.net", "token": "env:T"}`

func TestDNS01ChallengeDelegationDecouplesTheZone(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	// The certificate names live in example.com, but the provider only has
	// delegated.net: valid because the challenge is written to the target.
	cfg := dns01Config(t, delegatedTLS(hetznerForDelegated, ""))
	got := cfg.TLS.DNS01.ChallengeDelegation
	if len(got) != 1 || got["example.com"] != "example-com.acme.delegated.net" {
		t.Fatalf("delegation = %v, want normalized", got)
	}
	clone := Clone(*cfg)
	clone.TLS.DNS01.ChallengeDelegation["example.com"] = "elsewhere.test"
	if cfg.TLS.DNS01.ChallengeDelegation["example.com"] != "example-com.acme.delegated.net" {
		t.Fatal("Clone shares the delegation map")
	}
}

func TestDNS01ChallengeDelegationRefusals(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	load := func(tlsJSON string) error {
		path := writeConfig(t, `{"listen": ":8888", "tls": `+tlsJSON+`, "routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]}`)
		_, err := Load(path)
		return err
	}
	cloudflare := `"dns_token": "env:T"`
	cases := map[string]struct{ tls, want string }{
		"base is not a certificate name": {strings.Replace(delegatedTLS(cloudflare, ""), `"Example.COM"`, `"other.org"`, 1), "challenge_delegation"},
		"wildcard base":                  {strings.Replace(delegatedTLS(cloudflare, ""), `"Example.COM"`, `"*.example.com"`, 1), "challenge_delegation"},
		"empty target":                   {strings.Replace(delegatedTLS(cloudflare, ""), `"example-com.acme.delegated.net."`, `""`, 1), "challenge_delegation"},
		"target is the challenge name":   {strings.Replace(delegatedTLS(cloudflare, ""), `"example-com.acme.delegated.net."`, `"_acme-challenge.example.com"`, 1), "challenge_delegation"},
		"invalid target":                 {strings.Replace(delegatedTLS(cloudflare, ""), `"example-com.acme.delegated.net."`, `"bad name.net"`, 1), "challenge_delegation"},
		"zone does not contain target":   {strings.Replace(delegatedTLS(hetznerForDelegated, ""), `"example-com.acme.delegated.net."`, `"x.other.org"`, 1), "zone"},
	}
	for name, tc := range cases {
		err := load(tc.tls)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want mention of %q", name, err, tc.want)
		}
	}
	// Without a delegation the same provider/zone mismatch is still refused.
	plain := strings.Replace(delegatedTLS(hetznerForDelegated, ""), `"challenge_delegation": {"Example.COM": "example-com.acme.delegated.net."}`, `"challenge_delegation": {}`, 1)
	if err := load(plain); err == nil || !strings.Contains(err.Error(), "zone") {
		t.Errorf("provider zone must still have to contain the names: %v", err)
	}
}

func builtinTLS(mutate func(string) string) string {
	base := `{
		"enabled": true,
		"domains": ["*.example.com", "example.com"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/ozal/certs",
			"directory_url": "https://acme-v02.api.letsencrypt.org/directory",
			"dns_provider": "builtin",
			"builtin": {"zone": "Acme.Example.NET.", "listen": "203.0.113.7:53", "nameservers": ["ns1.example.net."]},
			"challenge_delegation": {"example.com": "example-com.acme.example.net"}
		}
	}`
	if mutate != nil {
		return mutate(base)
	}
	return base
}

func TestDNS01ConfigAcceptsBuiltin(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, builtinTLS(nil))
	b := cfg.TLS.DNS01.Builtin
	if cfg.TLS.DNS01.DNSProvider != "builtin" || b == nil {
		t.Fatalf("dns01 = %+v", cfg.TLS.DNS01)
	}
	if b.Zone != "acme.example.net" || b.Listen != "203.0.113.7:53" || len(b.Nameservers) != 1 || b.Nameservers[0] != "ns1.example.net" {
		t.Fatalf("builtin = %+v, want normalized", b)
	}
	clone := Clone(*cfg)
	clone.TLS.DNS01.Builtin.Zone = "elsewhere.test"
	clone.TLS.DNS01.Builtin.Nameservers[0] = "ns9.test"
	if cfg.TLS.DNS01.Builtin.Zone != "acme.example.net" || cfg.TLS.DNS01.Builtin.Nameservers[0] != "ns1.example.net" {
		t.Fatal("Clone shares the builtin block")
	}
}

func TestDNS01BuiltinRefusals(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	load := func(tlsJSON string) error {
		path := writeConfig(t, `{"listen": ":8888", "tls": `+tlsJSON+`, "routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]}`)
		_, err := Load(path)
		return err
	}
	replace := func(old, new string) string {
		return builtinTLS(func(s string) string { return strings.Replace(s, old, new, 1) })
	}
	cases := map[string]struct{ tls, want string }{
		"block missing":       {replace(`"builtin": {"zone": "Acme.Example.NET.", "listen": "203.0.113.7:53", "nameservers": ["ns1.example.net."]},`, ""), "builtin"},
		"wildcard listen":     {replace(`203.0.113.7:53`, `:53`), "listen"},
		"unspecified address": {replace(`203.0.113.7:53`, `0.0.0.0:53`), "listen"},
		"hostname as listen":  {replace(`203.0.113.7:53`, `ns1.example.net:53`), "listen"},
		"port zero":           {replace(`203.0.113.7:53`, `203.0.113.7:0`), "listen"},
		"no nameservers":      {replace(`["ns1.example.net."]`, `[]`), "nameservers"},
		"invalid nameserver":  {replace(`"ns1.example.net."`, `"bad ns"`), "nameserver"},
		"single-label zone":   {replace(`"Acme.Example.NET."`, `"net"`), "zone"},
		"no delegation": {replace(`,
			"challenge_delegation": {"example.com": "example-com.acme.example.net"}`, ``), "challenge_delegation"},
		"target outside zone":     {replace(`"example-com.acme.example.net"`, `"x.other.org"`), "zone"},
		"target is the apex":      {replace(`"example-com.acme.example.net"`, `"acme.example.net"`), "zone"},
		"uncovered domain":        {replace(`"domains": ["*.example.com", "example.com"]`, `"domains": ["*.example.com", "example.com", "example.org"]`), "example.org"},
		"with a cloudflare token": {replace(`"dns_provider": "builtin",`, `"dns_provider": "builtin", "dns_token": "env:T",`), "dns_token"},
		"cloudflare with builtin": {replace(`"dns_provider": "builtin",`, `"dns_token": "env:T",`), "builtin"},
		"hetzner with builtin":    {replace(`"dns_provider": "builtin",`, `"dns_provider": "hetzner", "hetzner": {"zone": "example.net", "token": "env:T"},`), "builtin"},
		"builtin with hetzner":    {replace(`"builtin": {`, `"hetzner": {"zone": "example.net", "token": "env:T"}, "builtin": {`), "hetzner"},
	}
	for name, tc := range cases {
		err := load(tc.tls)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want mention of %q", name, err, tc.want)
		}
	}
}

func TestDNS01ConfigAcceptsSeveralDomainsOnOneCertificate(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{
		"enabled": true,
		"domains": ["*.octaviosantacruz.com", "octaviosantacruz.com", "ozal.dev", "sboard.dev", "sigea.app"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/ozal/certs",
			"directory_url": "https://acme-staging-v02.api.letsencrypt.org/directory",
			"dns_token": "env:CLOUDFLARE_DNS_TOKEN"
		}
	}`)
	if got := len(cfg.TLS.Domains); got != 5 {
		t.Fatalf("domains = %d, want the five names kept", got)
	}
}
