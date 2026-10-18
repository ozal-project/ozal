package certs

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeDNS is an in-memory DNS-01 provider. It can fail a create cleanly, or
// insert the record and still report the outcome as unknown — the exact
// situation that must never lead to a blind delete.
type fakeDNS struct {
	mu      sync.Mutex
	records map[string]DNSRecord
	nextID  int

	createErr        error
	insertBeforeErr  bool
	deleteErr        error
	created, deleted int
}

func newFakeDNS() *fakeDNS {
	return &fakeDNS{records: map[string]DNSRecord{}}
}

func (f *fakeDNS) CreateTXT(ctx context.Context, name, value string) (DNSRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertBeforeErr {
		f.nextID++
		id := fmt.Sprintf("record-%d", f.nextID)
		f.records[id] = DNSRecord{ZoneID: "zone-1", ID: id, Name: name, Value: value}
		f.created++
		return DNSRecord{}, fmt.Errorf("%w: the response was lost", ErrAmbiguous)
	}
	if f.createErr != nil {
		return DNSRecord{}, f.createErr
	}
	f.nextID++
	id := fmt.Sprintf("record-%d", f.nextID)
	record := DNSRecord{ZoneID: "zone-1", ID: id, Name: name, Value: value}
	f.records[id] = record
	f.created++
	return record, nil
}

func (f *fakeDNS) DeleteTXT(ctx context.Context, record DNSRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.records[record.ID]; !ok {
		return nil
	}
	delete(f.records, record.ID)
	f.deleted++
	return nil
}

func (f *fakeDNS) snapshot() []DNSRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]DNSRecord, 0, len(f.records))
	for _, record := range f.records {
		out = append(out, record)
	}
	return out
}

func (f *fakeDNS) values(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, record := range f.records {
		if record.Name == name {
			out = append(out, record.Value)
		}
	}
	return out
}

// fakeResolver serves the fake DNS provider's current records, and can
// withhold them for the first N lookups to exercise propagation polling.
type fakeResolver struct {
	dns          *fakeDNS
	mu           sync.Mutex
	lookups      int
	visibleAfter int
}

func (r *fakeResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	r.mu.Lock()
	r.lookups++
	visible := r.visibleAfter == 0 || r.lookups > r.visibleAfter
	r.mu.Unlock()
	if !visible {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	return r.dns.values(name), nil
}

func (r *fakeResolver) lookupCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lookups
}

// fakeACME is a minimal RFC 8555 server for the DNS-01 flow: directory,
// account, order, one authorization with a dns-01 challenge, finalize and
// certificate. Signatures are not verified — the goal is to exercise the
// real x/crypto/acme client's protocol handling, not to reimplement a CA.
type fakeACME struct {
	mu       sync.Mutex
	base     string
	host     string
	nonces   int
	authz    string // pending | valid | invalid
	reject   bool
	retry    int // when set, new-order answers 429 with this Retry-After
	accounts int // new-account calls observed
	orders   int // new-order calls observed
	caKey    *ecdsa.PrivateKey
	caCert   *x509.Certificate

	certificatePEM []byte // filled in on finalize
}

func newFakeACME(t *testing.T, host string) (*fakeACME, *httptest.Server) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake acme CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	f := &fakeACME{host: host, authz: "pending", caKey: caKey, caCert: caCert}
	server := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(server.Close)
	f.base = server.URL
	return f, server
}

func (f *fakeACME) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.nonces++
	w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", f.nonces))
	f.mu.Unlock()

	switch r.URL.Path {
	case "/directory":
		writeJSON(w, http.StatusOK, map[string]any{
			"newNonce":   f.base + "/new-nonce",
			"newAccount": f.base + "/new-account",
			"newOrder":   f.base + "/new-order",
		})
	case "/new-nonce":
		w.WriteHeader(http.StatusOK)
	case "/new-account":
		f.mu.Lock()
		f.accounts++
		f.mu.Unlock()
		w.Header().Set("Location", f.base+"/acct/1")
		writeJSON(w, http.StatusCreated, map[string]any{"status": "valid", "contact": []string{}})
	case "/new-order":
		f.mu.Lock()
		f.orders++
		retry := f.retry
		f.authz = "pending"
		f.mu.Unlock()
		if retry > 0 {
			w.Header().Set("Retry-After", fmt.Sprint(retry))
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"urn:ietf:params:acme:error:rateLimited","detail":"too many new orders"}`))
			return
		}
		w.Header().Set("Location", f.base+"/order/1")
		writeJSON(w, http.StatusCreated, map[string]any{
			"status":         "pending",
			"identifiers":    []map[string]string{{"type": "dns", "value": f.host}},
			"authorizations": []string{f.base + "/authz/1"},
			"finalize":       f.base + "/finalize/1",
		})
	case "/authz/1":
		f.mu.Lock()
		status := f.authz
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     status,
			"identifier": map[string]string{"type": "dns", "value": f.host},
			"challenges": []map[string]any{{
				"type":   "dns-01",
				"url":    f.base + "/chall/1",
				"token":  "challenge-token",
				"status": status,
			}},
		})
	case "/chall/1":
		f.mu.Lock()
		if f.reject {
			f.authz = "invalid"
		} else {
			f.authz = "valid"
		}
		status := f.authz
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"type":   "dns-01",
			"url":    f.base + "/chall/1",
			"token":  "challenge-token",
			"status": status,
		})
	case "/finalize/1":
		f.mu.Lock()
		reject := f.reject
		f.mu.Unlock()
		if reject {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"type":"urn:ietf:params:acme:error:unauthorized","detail":"authorization is not valid"}`))
			return
		}
		payload, err := jwsPayload(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body struct {
			CSR string `json:"csr"`
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		csrDER, err := base64.RawURLEncoding.DecodeString(body.CSR)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		leafPEM, err := f.signCSR(csrDER)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.mu.Lock()
		f.certificatePEM = leafPEM
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "valid",
			"certificate": f.base + "/cert/1",
			"finalize":    f.base + "/finalize/1",
		})
	case "/cert/1":
		f.mu.Lock()
		chain := f.certificatePEM
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(chain)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// signCSR issues a leaf for the fake CA, the way a real CA would answer a
// finalize request.
func (f *fakeACME) signCSR(csrDER []byte) ([]byte, error) {
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      csr.Subject,
		DNSNames:     csr.DNSNames,
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, f.caCert, csr.PublicKey, f.caKey)
	if err != nil {
		return nil, err
	}
	return append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.caCert.Raw})...,
	), nil
}

func (f *fakeACME) orderCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.orders
}

func (f *fakeACME) accountCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accounts
}

func jwsPayload(r *http.Request) ([]byte, error) {
	var envelope struct {
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	return base64.RawURLEncoding.DecodeString(envelope.Payload)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// testACMEIssuer wires the issuer with the fake ACME server, the fake DNS
// provider and a real file store under t.TempDir().
func testACMEIssuer(t *testing.T, server *httptest.Server, dns DNSProvider, resolver TXTResolver, cfg ACMEConfig) (*ACMEIssuer, *FileStore) {
	t.Helper()
	store := testStore(t)
	cfg.DirectoryURL = server.URL + "/directory"
	if cfg.Email == "" {
		cfg.Email = "ops@example.com"
	}
	cfg.DNS = dns
	cfg.Resolver = resolver
	cfg.HTTPClient = server.Client()
	if cfg.PropagationInterval <= 0 {
		cfg.PropagationInterval = time.Millisecond
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	issuer, err := NewACMEIssuer(cfg, store)
	if err != nil {
		t.Fatalf("NewACMEIssuer: %v", err)
	}
	return issuer, store
}

func testSigningKey(t *testing.T) crypto.Signer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestACMEIssuerIssuesAndCleansUp(t *testing.T) {
	host := "preview.example.com"
	fake, server := newFakeACME(t, host)
	defer func() {
		if orders := fake.orderCount(); orders != 1 {
			t.Errorf("new-order calls = %d, want 1", orders)
		}
	}()
	dns := newFakeDNS()
	// An unrelated TXT record at the same name must survive every step.
	unrelated := DNSRecord{ZoneID: "zone-1", ID: "unrelated-1", Name: "_acme-challenge." + host, Value: "somebody-elses-value"}
	dns.records[unrelated.ID] = unrelated
	resolver := &fakeResolver{dns: dns}

	issuer, store := testACMEIssuer(t, server, dns, resolver, ACMEConfig{})
	gen, err := issuer.Obtain(context.Background(), host, testSigningKey(t))
	if err != nil {
		t.Fatalf("Obtain: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(fake.caCert)
	if _, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, host, time.Now(), roots); err != nil {
		t.Fatalf("issued generation is not usable: %v", err)
	}

	// The challenge record is gone; the unrelated one is untouched.
	records := dns.snapshot()
	if len(records) != 1 || records[0].ID != unrelated.ID {
		t.Fatalf("records after issuance = %+v, want only the unrelated one", records)
	}
	intents, err := store.ListIntents(context.Background())
	if err != nil || len(intents) != 0 {
		t.Fatalf("intents after issuance = %d, err %v", len(intents), err)
	}
	// The account was persisted for this directory only.
	account, ok, err := store.LoadAccount(context.Background(), server.URL+"/directory")
	if err != nil || !ok {
		t.Fatalf("account = ok %v, err %v", ok, err)
	}
	if account.KID != server.URL+"/acct/1" {
		t.Fatalf("account KID = %q", account.KID)
	}
}

func TestACMEIssuerReusesThePersistedAccount(t *testing.T) {
	host := "preview.example.com"
	fake, server := newFakeACME(t, host)
	dns := newFakeDNS()
	issuer, _ := testACMEIssuer(t, server, dns, &fakeResolver{dns: dns}, ACMEConfig{})
	ctx := context.Background()
	if _, err := issuer.Obtain(ctx, host, testSigningKey(t)); err != nil {
		t.Fatalf("first Obtain: %v", err)
	}
	if _, err := issuer.Obtain(ctx, host, testSigningKey(t)); err != nil {
		t.Fatalf("second Obtain: %v", err)
	}
	if accounts := fake.accountCount(); accounts != 1 {
		t.Fatalf("new-account calls = %d, want the persisted account reused", accounts)
	}
	if orders := fake.orderCount(); orders != 2 {
		t.Fatalf("new-order calls = %d, want one per issuance", orders)
	}
}

func TestACMEIssuerCleansUpAfterValidationFailure(t *testing.T) {
	host := "preview.example.com"
	fake, server := newFakeACME(t, host)
	fake.reject = true
	dns := newFakeDNS()
	unrelated := DNSRecord{ZoneID: "zone-1", ID: "unrelated-1", Name: "_acme-challenge." + host, Value: "other"}
	dns.records[unrelated.ID] = unrelated

	issuer, store := testACMEIssuer(t, server, dns, &fakeResolver{dns: dns}, ACMEConfig{})
	if _, err := issuer.Obtain(context.Background(), host, testSigningKey(t)); err == nil {
		t.Fatal("Obtain must fail when the authorization is rejected")
	}
	records := dns.snapshot()
	if len(records) != 1 || records[0].ID != unrelated.ID {
		t.Fatalf("records after failure = %+v, want only the unrelated one", records)
	}
	if intents, err := store.ListIntents(context.Background()); err != nil || len(intents) != 0 {
		t.Fatalf("intents after failure = %d, err %v", len(intents), err)
	}
}

func TestACMEIssuerWaitsForPropagation(t *testing.T) {
	host := "preview.example.com"
	_, server := newFakeACME(t, host)
	dns := newFakeDNS()
	resolver := &fakeResolver{dns: dns, visibleAfter: 2}
	issuer, _ := testACMEIssuer(t, server, dns, resolver, ACMEConfig{})
	if _, err := issuer.Obtain(context.Background(), host, testSigningKey(t)); err != nil {
		t.Fatalf("Obtain: %v", err)
	}
	if lookups := resolver.lookupCount(); lookups <= 2 {
		t.Fatalf("lookups = %d, want polling before the value was visible", lookups)
	}
}

func TestACMEIssuerPropagationTimeoutFailsTheAttempt(t *testing.T) {
	host := "preview.example.com"
	_, server := newFakeACME(t, host)
	dns := newFakeDNS()
	resolver := &fakeResolver{dns: dns, visibleAfter: 1_000_000}
	issuer, store := testACMEIssuer(t, server, dns, resolver, ACMEConfig{
		PropagationTimeout: 30 * time.Millisecond,
	})
	_, err := issuer.Obtain(context.Background(), host, testSigningKey(t))
	if !errors.Is(err, ErrDNS) {
		t.Fatalf("err = %v, want ErrDNS", err)
	}
	if records := dns.snapshot(); len(records) != 0 {
		t.Fatalf("the challenge record must be cleaned up after a timeout: %+v", records)
	}
	if intents, err := store.ListIntents(context.Background()); err != nil || len(intents) != 0 {
		t.Fatalf("intents after timeout = %d, err %v", len(intents), err)
	}
}

func TestACMEIssuerKeepsAmbiguousCreatesForRecovery(t *testing.T) {
	host := "preview.example.com"
	_, server := newFakeACME(t, host)
	dns := newFakeDNS()
	dns.insertBeforeErr = true // the record exists, but we never learn its ID
	issuer, store := testACMEIssuer(t, server, dns, &fakeResolver{dns: dns}, ACMEConfig{})

	_, err := issuer.Obtain(context.Background(), host, testSigningKey(t))
	if !errors.Is(err, ErrDNS) {
		t.Fatalf("err = %v, want ErrDNS", err)
	}
	// The record may exist, so it is left alone — but its intent records the
	// situation instead of hiding it.
	if records := dns.snapshot(); len(records) != 1 {
		t.Fatalf("records = %+v, want the ambiguous record preserved", records)
	}
	intents, err := store.ListIntents(context.Background())
	if err != nil || len(intents) != 1 {
		t.Fatalf("intents = %d, err %v; want the ambiguous intent kept", len(intents), err)
	}
	if !intents[0].Ambiguous || intents[0].RecordID != "" || intents[0].Name != "_acme-challenge."+host {
		t.Fatalf("intent = %+v", intents[0])
	}
	pending, err := issuer.Recover(context.Background())
	if err != nil || pending != 1 {
		t.Fatalf("Recover = %d, %v; want one pending intent reported", pending, err)
	}
	if records := dns.snapshot(); len(records) != 1 {
		t.Fatal("recovery must never delete a record it cannot identify")
	}
}

func TestACMEIssuerRecoversOrphanedRecordsByID(t *testing.T) {
	ctx := context.Background()
	host := "preview.example.com"
	_, server := newFakeACME(t, host)
	dns := newFakeDNS()
	orphan, err := dns.CreateTXT(ctx, "_acme-challenge."+host, "orphan-value")
	if err != nil {
		t.Fatal(err)
	}
	issuer, store := testACMEIssuer(t, server, dns, &fakeResolver{dns: dns}, ACMEConfig{})
	if _, err := store.SaveIntent(ctx, DNSIntent{
		Name: "_acme-challenge." + host, Value: "orphan-value", ZoneID: orphan.ZoneID, RecordID: orphan.ID,
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := issuer.Recover(ctx)
	if err != nil || pending != 0 {
		t.Fatalf("Recover = %d, %v; want a clean recovery", pending, err)
	}
	if records := dns.snapshot(); len(records) != 0 {
		t.Fatalf("records = %+v, want the orphan deleted", records)
	}
	if intents, err := store.ListIntents(ctx); err != nil || len(intents) != 0 {
		t.Fatalf("intents = %d, err %v", len(intents), err)
	}
}

func TestACMEIssuerReportsRateLimits(t *testing.T) {
	host := "preview.example.com"
	fake, server := newFakeACME(t, host)
	fake.retry = 120
	dns := newFakeDNS()
	issuer, _ := testACMEIssuer(t, server, dns, &fakeResolver{dns: dns}, ACMEConfig{})
	_, err := issuer.Obtain(context.Background(), host, testSigningKey(t))
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("err = %v, want RateLimitedError", err)
	}
	if limited.RetryAfter != 2*time.Minute {
		t.Fatalf("retry after = %s, want 2m", limited.RetryAfter)
	}
}
