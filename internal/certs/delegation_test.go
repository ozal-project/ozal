package certs

import (
	"context"
	"crypto/x509"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

const delegatedTarget = "example-com.acme.delegated.net"

// cnameResolver models a resolver that follows CNAMEs: a lookup of the name
// the CA queries lands on the delegated target, where the provider wrote the
// record. cnames maps a name to its CNAME target.
type cnameResolver struct {
	dns      *fakeDNS
	cnames   map[string]string
	cnameErr error
}

func normalizeLookup(name string) string { return strings.TrimSuffix(strings.ToLower(name), ".") }

func (r *cnameResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	name = normalizeLookup(name)
	if target, ok := r.cnames[name]; ok {
		name = target
	}
	return r.dns.values(name), nil
}

func (r *cnameResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	if r.cnameErr != nil {
		return "", r.cnameErr
	}
	host = normalizeLookup(host)
	if target, ok := r.cnames[host]; ok {
		return target + ".", nil
	}
	return host + ".", nil // no CNAME: the canonical name is the name itself
}

func TestDelegatedChallengeIsWrittenToTheTargetZone(t *testing.T) {
	fake, server := newFakeACME(t, apexNames[0], apexNames[1:]...)
	dns := newFakeDNS()
	resolver := &cnameResolver{dns: dns, cnames: map[string]string{"_acme-challenge.example.com": delegatedTarget}}
	issuer, store := testACMEIssuer(t, server, dns, resolver, ACMEConfig{
		Delegations: map[string]string{"Example.COM": delegatedTarget + "."},
	})

	gen, err := issuer.Obtain(context.Background(), apexNames, testSigningKey(t))
	if err != nil {
		t.Fatalf("Obtain: %v", err)
	}
	want := []string{delegatedTarget, delegatedTarget}
	if !slices.Equal(dns.createdNames, want) {
		t.Fatalf("challenge records created at %v, want %v: nothing may be written to the delegating zone", dns.createdNames, want)
	}
	if dns.peak != 1 || len(dns.snapshot()) != 0 {
		t.Fatalf("peak = %d, left = %+v", dns.peak, dns.snapshot())
	}
	if intents, err := store.ListIntents(context.Background()); err != nil || len(intents) != 0 {
		t.Fatalf("intents = %d, err %v", len(intents), err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(fake.caCert)
	if _, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, apexNames, time.Now(), roots); err != nil {
		t.Fatalf("issued generation is not usable: %v", err)
	}
}

func TestDelegationFailsEarlyWhenTheCNAMEIsNotInPlace(t *testing.T) {
	for name, resolver := range map[string]func(*fakeDNS) *cnameResolver{
		"no CNAME at all": func(d *fakeDNS) *cnameResolver { return &cnameResolver{dns: d, cnames: map[string]string{}} },
		"points elsewhere": func(d *fakeDNS) *cnameResolver {
			return &cnameResolver{dns: d, cnames: map[string]string{"_acme-challenge.example.com": "someone-else.example.net"}}
		},
		"lookup fails": func(d *fakeDNS) *cnameResolver {
			return &cnameResolver{dns: d, cnames: map[string]string{}, cnameErr: errors.New("servfail")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, server := newFakeACME(t, apexNames[0], apexNames[1:]...)
			dns := newFakeDNS()
			issuer, store := testACMEIssuer(t, server, dns, resolver(dns), ACMEConfig{
				Delegations: map[string]string{"example.com": delegatedTarget},
			})
			_, err := issuer.Obtain(context.Background(), apexNames, testSigningKey(t))
			if !errors.Is(err, ErrDNS) || errors.Is(err, ErrAmbiguous) {
				t.Fatalf("err = %v, want a clean DNS failure", err)
			}
			if !strings.Contains(err.Error(), "_acme-challenge.example.com") || !strings.Contains(err.Error(), delegatedTarget) {
				t.Fatalf("the error must name the CNAME to create: %v", err)
			}
			if dns.created != 0 {
				t.Fatalf("%d records were created before the CNAME was proven", dns.created)
			}
			if intents, _ := store.ListIntents(context.Background()); len(intents) != 0 {
				t.Fatalf("intents left behind: %d", len(intents))
			}
		})
	}
}

func TestDelegationRequiresAResolverThatReadsCNAMEs(t *testing.T) {
	dns := newFakeDNS()
	_, err := NewACMEIssuer(ACMEConfig{
		DirectoryURL: "https://acme.example/directory", Email: "ops@example.com", DNS: dns,
		Resolver:    &fakeResolver{dns: dns}, // LookupTXT only
		Delegations: map[string]string{"example.com": delegatedTarget},
	}, testStore(t))
	if err == nil {
		t.Fatal("delegation without a way to verify the CNAME must be refused at composition")
	}
}

func TestDelegationEntriesAreValidated(t *testing.T) {
	dns := newFakeDNS()
	for name, delegations := range map[string]map[string]string{
		"empty target":         {"example.com": ""},
		"empty base":           {"": delegatedTarget},
		"wildcard base":        {"*.example.com": delegatedTarget},
		"target is the source": {"example.com": "_acme-challenge.example.com"},
		"target under source":  {"example.com": "x._acme-challenge.example.com"},
		"invalid target":       {"example.com": "bad name.example.net"},
	} {
		_, err := NewACMEIssuer(ACMEConfig{
			DirectoryURL: "https://acme.example/directory", Email: "ops@example.com", DNS: dns,
			Resolver: &cnameResolver{dns: dns}, Delegations: delegations,
		}, testStore(t))
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNoDelegationKeepsTheChallengeAtTheDomain(t *testing.T) {
	_, server := newFakeACME(t, "preview.example.com")
	dns := newFakeDNS()
	issuer, _ := testACMEIssuer(t, server, dns, &cnameResolver{dns: dns}, ACMEConfig{
		Delegations: map[string]string{"other.org": delegatedTarget},
	})
	if _, err := issuer.Obtain(context.Background(), []string{"preview.example.com"}, testSigningKey(t)); err != nil {
		t.Fatal(err)
	}
	if want := []string{"_acme-challenge.preview.example.com"}; !slices.Equal(dns.createdNames, want) {
		t.Fatalf("created at %v, want %v", dns.createdNames, want)
	}
}

// A zone added at runtime has no explicit delegation: with a suffix (the
// builtin DNS zone) its challenge goes to "<domain>.<suffix>", only once
// the operator's CNAME points there.
func TestDelegationSuffixDelegatesZonesWithoutAnEntry(t *testing.T) {
	_, server := newFakeACME(t, apexNames[0], apexNames[1:]...)
	dns := newFakeDNS()
	target := "example.com.acme.example.net"
	resolver := &cnameResolver{dns: dns, cnames: map[string]string{"_acme-challenge.example.com": target}}
	issuer, _ := testACMEIssuer(t, server, dns, resolver, ACMEConfig{DelegationSuffix: "ACME.example.net."})
	if _, err := issuer.Obtain(context.Background(), apexNames, testSigningKey(t)); err != nil {
		t.Fatalf("Obtain: %v", err)
	}
	if want := []string{target, target}; !slices.Equal(dns.createdNames, want) {
		t.Fatalf("records created at %v, want %v", dns.createdNames, want)
	}

	_, server = newFakeACME(t, apexNames[0], apexNames[1:]...)
	dns = newFakeDNS()
	missing := &cnameResolver{dns: dns, cnames: map[string]string{}}
	issuer, _ = testACMEIssuer(t, server, dns, missing, ACMEConfig{DelegationSuffix: "acme.example.net"})
	if _, err := issuer.Obtain(context.Background(), apexNames, testSigningKey(t)); err == nil {
		t.Fatal("issued without the CNAME in place")
	}
	if len(dns.createdNames) != 0 {
		t.Fatalf("records written before the CNAME was verified: %v", dns.createdNames)
	}
}
