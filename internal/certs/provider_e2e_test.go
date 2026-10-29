package certs

import (
	"context"
	"crypto/x509"
	"net"
	"sort"
	"strings"
	"testing"
	"time"
)

// simulatedZone is the authoritative side of one adapter under test: it
// serves what a resolver would see and lets the test plant a record that
// belongs to somebody else.
type simulatedZone struct {
	provider DNSProvider
	lookup   func(name string) []string
	plant    func(name, value string)
}

type zoneResolver struct{ zone simulatedZone }

func (r zoneResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	values := r.zone.lookup(strings.TrimSuffix(name, "."))
	if len(values) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	return values, nil
}

// TestProvidersDriveARealIssuance plugs each adapter into the real ACME
// issuer against the simulated CA: the certificate must be usable, the
// challenge must have been visible to the resolver during validation, and
// afterwards only the record somebody else planted may remain.
func TestProvidersDriveARealIssuance(t *testing.T) {
	const host = "preview.example.test"
	const name = "_acme-challenge." + host
	const foreign = "somebody-elses-value"

	build := map[string]func(t *testing.T) simulatedZone{
		"rfc2136": func(t *testing.T) simulatedZone {
			s := newFakeUpdateServer(t, time.Now())
			return simulatedZone{
				provider: newTestClient(t, s, func(c *RFC2136Config) { c.Now = time.Now }),
				lookup: func(n string) []string {
					s.mu.Lock()
					defer s.mu.Unlock()
					var out []string
					for v := range s.txt[n] {
						out = append(out, v)
					}
					return out
				},
				plant: func(n, v string) {
					s.mu.Lock()
					defer s.mu.Unlock()
					if s.txt[n] == nil {
						s.txt[n] = map[string]bool{}
					}
					s.txt[n][v] = true
				},
			}
		},
		"hetzner": func(t *testing.T) simulatedZone {
			f := newFakeHetzner(t)
			f.slow = 1
			rel := func(n string) string { return strings.TrimSuffix(n, ".example.test") }
			return simulatedZone{
				provider: f.client(t, nil),
				lookup: func(n string) []string {
					f.mu.Lock()
					defer f.mu.Unlock()
					var out []string
					for v := range f.sets[rel(n)+"/TXT"] {
						// A resolver returns the TXT content, not its quoting.
						out = append(out, strings.Trim(v, `"`))
					}
					return out
				},
				plant: func(n, v string) {
					f.mu.Lock()
					defer f.mu.Unlock()
					key := rel(n) + "/TXT"
					if f.sets[key] == nil {
						f.sets[key] = map[string]bool{}
					}
					f.sets[key][`"`+v+`"`] = true
				},
			}
		},
	}

	for provider, newZone := range build {
		t.Run(provider, func(t *testing.T) {
			zone := newZone(t)
			zone.plant(name, foreign)
			fake, server := newFakeACME(t, host)
			resolver := &observedResolver{inner: zoneResolver{zone}}
			issuer, store := testACMEIssuer(t, server, zone.provider, resolver, ACMEConfig{PropagationTimeout: 5 * time.Second})

			gen, err := issuer.Obtain(context.Background(), []string{host}, testSigningKey(t))
			if err != nil {
				t.Fatalf("Obtain: %v", err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(fake.caCert)
			if _, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, []string{host}, time.Now(), roots); err != nil {
				t.Fatalf("issued generation is not usable: %v", err)
			}

			if len(resolver.sawChallenge(foreign)) == 0 {
				t.Fatal("the resolver never saw the challenge record: validation did not depend on the provider")
			}
			left := zone.lookup(name)
			sort.Strings(left)
			if len(left) != 1 || left[0] != foreign {
				t.Fatalf("records after issuance = %v, want only the foreign one", left)
			}
			intents, err := store.ListIntents(context.Background())
			if err != nil || len(intents) != 0 {
				t.Fatalf("intents after issuance = %d, err %v", len(intents), err)
			}
		})
	}
}

// observedResolver remembers every value it returned, so the test can tell
// that the challenge was really published while ACME validated.
type observedResolver struct {
	inner TXTResolver
	seen  []string
}

func (r *observedResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	values, err := r.inner.LookupTXT(ctx, name)
	r.seen = append(r.seen, values...)
	return values, err
}

// sawChallenge returns the values seen other than the excluded one.
func (r *observedResolver) sawChallenge(exclude string) []string {
	var out []string
	for _, v := range r.seen {
		if v != exclude {
			out = append(out, v)
		}
	}
	return out
}
