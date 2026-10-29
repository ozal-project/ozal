package certs

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/ozal-project/ozal/internal/acmedns"
)

// dnsOverUDP is a resolver that asks the real in-process authoritative
// server over the network, and models the one CNAME the operator creates by
// hand at the delegating DNS: _acme-challenge.<domain> -> <target>.
type dnsOverUDP struct {
	t      *testing.T
	server string
	cnames map[string]string
}

func (r dnsOverUDP) follow(name string) string {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if target, ok := r.cnames[name]; ok {
		return target
	}
	return name
}

func (r dnsOverUDP) LookupTXT(_ context.Context, name string) ([]string, error) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 77})
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(r.follow(name) + "."), Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET})
	wire, _ := b.Finish()
	conn, err := net.Dial("udp", r.server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(wire); err != nil {
		return nil, err
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	var p dnsmessage.Parser
	h, err := p.Start(buf[:n])
	if err != nil {
		return nil, err
	}
	if h.RCode != dnsmessage.RCodeSuccess {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	_ = p.SkipAllQuestions()
	answers, err := p.AllAnswers()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range answers {
		if txt, ok := a.Body.(*dnsmessage.TXTResource); ok {
			out = append(out, strings.Join(txt.TXT, ""))
		}
	}
	return out, nil
}

func (r dnsOverUDP) LookupCNAME(_ context.Context, host string) (string, error) {
	return r.follow(host) + ".", nil
}

// TestBuiltinDNSDrivesARealIssuance issues a certificate for names whose
// own DNS is out of reach, using only a CNAME and Ozal's own server: the CA
// is simulated, but every DNS answer travels over a real socket.
func TestBuiltinDNSDrivesARealIssuance(t *testing.T) {
	zone, err := acmedns.NewZone("acme.example.test", []string{"ns1.example.test"}, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	store := acmedns.NewStore(zone)
	server, err := acmedns.Start(context.Background(), acmedns.Config{Zone: zone, Store: store, Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	provider, err := NewBuiltinDNS(store)
	if err != nil {
		t.Fatal(err)
	}

	names := []string{"*.example.com", "example.com"}
	fake, ca := newFakeACME(t, names[0], names[1:]...)
	resolver := dnsOverUDP{t: t, server: server.Addr().String(), cnames: map[string]string{
		"_acme-challenge.example.com": "example-com.acme.example.test",
	}}
	issuer, _ := testACMEIssuer(t, ca, provider, resolver, ACMEConfig{
		Delegations:        map[string]string{"example.com": "example-com.acme.example.test"},
		PropagationTimeout: 5 * time.Second,
	})
	gen, err := issuer.Obtain(context.Background(), names, testSigningKey(t))
	if err != nil {
		t.Fatalf("Obtain: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(fake.caCert)
	if _, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, names, time.Now(), roots); err != nil {
		t.Fatalf("issued generation is not usable: %v", err)
	}
	if got := store.TXT("example-com.acme.example.test"); len(got) != 0 {
		t.Fatalf("challenge records left published: %v", got)
	}
}

func TestBuiltinDNSRefusesRecordsItDidNotCreate(t *testing.T) {
	zone, _ := acmedns.NewZone("acme.example.test", []string{"ns1.example.test"}, "", 1)
	store := acmedns.NewStore(zone)
	provider, _ := NewBuiltinDNS(store)
	if _, err := provider.CreateTXT(context.Background(), "x.other.test", "v"); !errors.Is(err, ErrDNS) || errors.Is(err, ErrAmbiguous) {
		t.Fatalf("a name outside the zone must be a clean failure: %v", err)
	}
	rec, err := provider.CreateTXT(context.Background(), "x.acme.example.test", "v")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []DNSRecord{{ZoneID: "cloudflare", ID: rec.ID}, {ZoneID: "builtin"}} {
		if err := provider.DeleteTXT(context.Background(), bad); !errors.Is(err, ErrDNS) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	if len(store.TXT("x.acme.example.test")) != 1 {
		t.Fatal("a refused delete removed the record")
	}
	if err := provider.DeleteTXT(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBuiltinDNS(nil); err == nil {
		t.Fatal("a nil publisher must be refused")
	}
}
