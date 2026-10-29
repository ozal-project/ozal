package certs

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

// ACME issue timing where the defaults are not covered by the manager.
const (
	defaultPropagationTimeout  = 2 * time.Minute
	defaultPropagationInterval = 5 * time.Second
	defaultCleanupTimeout      = 30 * time.Second
	defaultACMEHTTPTimeout     = 30 * time.Second
)

// ACMEConfig configures the DNS-01 issuer. DirectoryURL and Email come from
// the TLS configuration; DNS and (optionally) Resolver and HTTPClient exist
// so tests can simulate the provider and the network without weakening
// anything.
type ACMEConfig struct {
	DirectoryURL string
	Email        string
	DNS          DNSProvider
	// Resolver looks up TXT records during propagation polling; nil selects
	// the system resolver.
	Resolver TXTResolver
	// HTTPClient talks to the ACME directory; nil selects a bounded default.
	HTTPClient *http.Client
	// Delegations maps a domain (the base of a certificate name) to the
	// name where its challenge TXT is written instead. The operator creates
	// one CNAME, _acme-challenge.<domain> -> target, at whatever DNS serves
	// the domain; from then on only the zone holding the target needs an
	// adapter. The CNAME is verified before any record is created.
	Delegations         map[string]string
	PropagationTimeout  time.Duration
	PropagationInterval time.Duration
	CleanupTimeout      time.Duration
	Now                 func() time.Time
	Logger              *slog.Logger
}

// ACMEIssuer speaks RFC 8555 DNS-01 through the existing x/crypto/acme
// client. It owns the account registration (persisted per directory) and the
// challenge records (one owned record at a time, cleaned up by ID on every
// path).
type ACMEIssuer struct {
	cfg   ACMEConfig
	state *FileStore

	mu     sync.Mutex
	client *acme.Client
}

// NewACMEIssuer validates the configuration and applies the defaults.
func NewACMEIssuer(cfg ACMEConfig, state *FileStore) (*ACMEIssuer, error) {
	if cfg.DirectoryURL == "" {
		return nil, errors.New("certs: acme directory URL is required")
	}
	if cfg.Email == "" {
		return nil, errors.New("certs: acme registration email is required")
	}
	if cfg.DNS == nil {
		return nil, errors.New("certs: acme issuer requires a DNS provider")
	}
	if state == nil {
		return nil, errors.New("certs: acme issuer requires the file store")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultACMEHTTPTimeout}
	}
	delegations, err := normalizeDelegations(cfg.Delegations)
	if err != nil {
		return nil, err
	}
	cfg.Delegations = delegations
	if len(delegations) > 0 {
		resolver := cfg.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		if _, ok := resolver.(CNAMEResolver); !ok {
			return nil, errors.New("certs: challenge delegation needs a resolver that can read CNAME records")
		}
	}
	if cfg.PropagationTimeout <= 0 {
		cfg.PropagationTimeout = defaultPropagationTimeout
	}
	if cfg.PropagationInterval <= 0 {
		cfg.PropagationInterval = defaultPropagationInterval
	}
	if cfg.CleanupTimeout <= 0 {
		cfg.CleanupTimeout = defaultCleanupTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &ACMEIssuer{cfg: cfg, state: state}, nil
}

// Obtain runs one issuance: create the order for every name, then for each
// authorization present a DNS-01 challenge record, wait until the value is
// visible, let the CA validate and clean up, then finalize and download the
// certificate. Authorizations run one after another, so a wildcard and its
// base, which share one challenge name, never have two records out at once.
func (i *ACMEIssuer) Obtain(ctx context.Context, names []string, key crypto.Signer) (StoredGeneration, error) {
	if len(names) == 0 || slices.Contains(names, "") || key == nil {
		return StoredGeneration{}, errors.New("certs: certificate names and private key are required")
	}
	client, err := i.clientFor(ctx)
	if err != nil {
		return StoredGeneration{}, err
	}

	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: names[0]},
		DNSNames: names,
	}, key)
	if err != nil {
		return StoredGeneration{}, fmt.Errorf("certs: build CSR: %w", err)
	}

	ids := make([]acme.AuthzID, 0, len(names))
	for _, name := range names {
		ids = append(ids, acme.AuthzID{Type: "dns", Value: name})
	}
	order, err := client.AuthorizeOrder(ctx, ids)
	if err != nil {
		return StoredGeneration{}, classifyACME(ctx, "authorize order", err)
	}
	for _, authzURL := range order.AuthzURLs {
		if err := i.authorize(ctx, client, names, authzURL); err != nil {
			return StoredGeneration{}, err
		}
	}

	der, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return StoredGeneration{}, classifyACME(ctx, "finalize order", err)
	}
	keyPEM, err := encodeKey(key)
	if err != nil {
		return StoredGeneration{}, err
	}
	return StoredGeneration{CertPEM: encodeChain(der), KeyPEM: keyPEM}, nil
}

// authorize presents and validates one authorization.
func (i *ACMEIssuer) authorize(ctx context.Context, client *acme.Client, names []string, authzURL string) error {
	authz, err := client.GetAuthorization(ctx, authzURL)
	if err != nil {
		return classifyACME(ctx, "read authorization", err)
	}
	if authz.Status == acme.StatusValid {
		return nil
	}
	index := slices.IndexFunc(authz.Challenges, func(c *acme.Challenge) bool { return c.Type == "dns-01" })
	if index < 0 {
		return fmt.Errorf("%w: the CA did not offer a dns-01 challenge", ErrDNS)
	}
	challenge := authz.Challenges[index]
	value, err := client.DNS01ChallengeRecord(challenge.Token)
	if err != nil {
		return fmt.Errorf("%w: derive challenge value: %v", ErrDNS, err)
	}

	// The CA names the identifier it validates: the base domain for a
	// wildcard. An authorization for a name we did not order is never
	// presented.
	got := authz.Identifier.Value
	index = slices.IndexFunc(names, func(name string) bool { return strings.EqualFold(got, baseOf(name)) })
	if index < 0 {
		return fmt.Errorf("%w: the CA authorized %q, which is not one of %v", ErrDNS, got, names)
	}
	fqdn := ChallengeFQDN(names[index]) // the name the CA queries
	target := fqdn                      // the name the record is written to
	if delegated, ok := i.cfg.Delegations[baseOf(names[index])]; ok {
		if err := i.checkDelegation(ctx, fqdn, delegated); err != nil {
			return err
		}
		target = delegated
	}
	record, intentID, err := i.present(ctx, target, value)
	if err != nil {
		return err
	}
	// Cleanup runs on every path — success, validation failure, cancellation
	// — with a fresh bounded context, because by the time a failure is
	// returned the parent context is often already cancelled.
	defer i.cleanup(record, intentID)

	if err := i.waitForRecord(ctx, fqdn, value); err != nil {
		return err
	}
	if _, err := client.Accept(ctx, challenge); err != nil {
		return classifyACME(ctx, "accept challenge", err)
	}
	if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
		return classifyACME(ctx, "wait authorization", err)
	}
	return nil
}

// checkDelegation proves the CNAME the operator was asked to create is in
// place, before any record is written. A missing or wrong CNAME would
// otherwise surface only as a validation failure at the CA, after a record
// was created in a zone the CA never reads.
func (i *ACMEIssuer) checkDelegation(ctx context.Context, fqdn, target string) error {
	canonical, err := i.resolver().(CNAMEResolver).LookupCNAME(ctx, fqdn)
	if err != nil || strings.TrimSuffix(strings.ToLower(canonical), ".") != target {
		return fmt.Errorf("%w: %s must be a CNAME to %s before the challenge can be delegated; create it at the DNS that serves the domain and retry", ErrDNS, fqdn, target)
	}
	return nil
}

// present writes the cleanup intent before the create call and reports back
// the owned record. The intent survives a crash between the API call and its
// response: an ambiguous outcome leaves the intent in place and lets
// cleanup/recovery decide, instead of guessing.
func (i *ACMEIssuer) present(ctx context.Context, fqdn, value string) (DNSRecord, string, error) {
	intentID, err := i.state.SaveIntent(ctx, DNSIntent{Name: fqdn, Value: value})
	if err != nil {
		return DNSRecord{}, "", err
	}
	record, err := i.cfg.DNS.CreateTXT(ctx, fqdn, value)
	if err != nil {
		if errors.Is(err, ErrAmbiguous) {
			i.markAmbiguous(ctx, intentID, fqdn, value)
			return DNSRecord{}, "", fmt.Errorf("%w: create %s: %v", ErrDNS, fqdn, err)
		}
		// A definite failure created nothing; drop the intent.
		if err := i.state.DeleteIntent(ctx, intentID); err != nil {
			i.cfg.Logger.Warn("cleanup intent could not be removed", "intent", intentID, "error", err)
		}
		return DNSRecord{}, "", fmt.Errorf("%w: create %s: %v", ErrDNS, fqdn, err)
	}
	// Record the exact owned ID. If this fails, the intent stays without an
	// ID: recovery will report it instead of deleting what it cannot name.
	if _, err := i.state.SaveIntent(ctx, DNSIntent{ID: intentID, Name: fqdn, Value: value, ZoneID: record.ZoneID, RecordID: record.ID}); err != nil {
		i.cfg.Logger.Warn("owned record ID could not be persisted; leaving the intent for review", "intent", intentID, "error", err)
	}
	return record, intentID, nil
}

// markAmbiguous records that a create call may have applied without a
// usable response. The intent keeps the name and value so an operator can
// recognize the record; recovery will not delete it.
func (i *ACMEIssuer) markAmbiguous(ctx context.Context, intentID, fqdn, value string) {
	if _, err := i.state.SaveIntent(ctx, DNSIntent{ID: intentID, Name: fqdn, Value: value, Ambiguous: true}); err != nil {
		i.cfg.Logger.Warn("ambiguous create could not be recorded", "intent", intentID, "error", err)
	}
}

// cleanup deletes exactly the record this attempt created, by its ID. An
// ambiguous create has no ID and is left alone on purpose.
func (i *ACMEIssuer) cleanup(record DNSRecord, intentID string) {
	if intentID == "" || record.ID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), i.cfg.CleanupTimeout)
	defer cancel()
	if err := i.cfg.DNS.DeleteTXT(ctx, record); err != nil {
		i.cfg.Logger.Warn("challenge record could not be deleted; recovery will retry", "name", record.Name, "error", err)
		return
	}
	if err := i.state.DeleteIntent(ctx, intentID); err != nil {
		i.cfg.Logger.Warn("cleanup intent could not be removed", "intent", intentID, "error", err)
	}
}

// Recover retries cleanup of intents earlier runs left behind: records with
// a known ID are deleted in their zone, ambiguous ones are reported and
// never guessed.
func (i *ACMEIssuer) Recover(ctx context.Context) (int, error) {
	intents, err := i.state.ListIntents(ctx)
	if err != nil {
		return 0, err
	}
	pending := 0
	var firstErr error
	for _, intent := range intents {
		if intent.RecordID == "" {
			pending++
			i.cfg.Logger.Warn("challenge record with unknown ID needs a manual look",
				"name", intent.Name, "created_at", intent.CreatedAt.UTC().Format(time.RFC3339))
			continue
		}
		record := DNSRecord{ZoneID: intent.ZoneID, ID: intent.RecordID, Name: intent.Name, Value: intent.Value}
		if err := i.cfg.DNS.DeleteTXT(ctx, record); err != nil {
			pending++
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: recovery delete %s: %v", ErrDNS, intent.Name, err)
			}
			continue
		}
		if err := i.state.DeleteIntent(ctx, intent.ID); err != nil {
			pending++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return pending, firstErr
}

// waitForRecord polls until the resolver sees the exact value. A local
// successful lookup is necessary, not sufficient: the CA validates from its
// own vantage points, so a failure observed there is retried as a whole
// attempt later.
func (i *ACMEIssuer) waitForRecord(ctx context.Context, name, value string) error {
	deadline := i.cfg.Now().Add(i.cfg.PropagationTimeout)
	for {
		if values, err := i.resolver().LookupTXT(ctx, name); err == nil && slices.Contains(values, value) {
			return nil
		}
		if !i.cfg.Now().Before(deadline) {
			return fmt.Errorf("%w: TXT record for %s was not visible within %s", ErrDNS, name, i.cfg.PropagationTimeout)
		}
		timer := time.NewTimer(i.cfg.PropagationInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (i *ACMEIssuer) resolver() TXTResolver {
	if i.cfg.Resolver != nil {
		return i.cfg.Resolver
	}
	return net.DefaultResolver
}

// clientFor loads or registers the ACME account exactly once per process.
// The account key and KID are persisted per directory, so staging and
// production never share an identity.
func (i *ACMEIssuer) clientFor(ctx context.Context) (*acme.Client, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.client != nil {
		return i.client, nil
	}

	account, ok, err := i.state.LoadAccount(ctx, i.cfg.DirectoryURL)
	if err != nil {
		return nil, err
	}
	if !ok {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("certs: generate acme account key: %w", err)
		}
		client := &acme.Client{Key: key, DirectoryURL: i.cfg.DirectoryURL, HTTPClient: i.cfg.HTTPClient, RetryBackoff: acmeRetryBackoff}
		registered, err := client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + i.cfg.Email}}, acme.AcceptTOS)
		if err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
			return nil, classifyACME(ctx, "register account", err)
		}
		if accountKeyID(registered, client) == "" {
			return nil, errors.New("certs: the ACME server returned no account URL")
		}
		keyPEM, err := encodeKey(key)
		if err != nil {
			return nil, err
		}
		if err := i.state.SaveAccount(ctx, ACMEAccount{
			DirectoryURL: i.cfg.DirectoryURL,
			Email:        i.cfg.Email,
			KID:          accountKeyID(registered, client),
			KeyPEM:       keyPEM,
		}); err != nil {
			return nil, err
		}
		i.client = client
		return client, nil
	}

	signer, err := parseSigner(account.KeyPEM)
	if err != nil {
		return nil, err
	}
	i.client = &acme.Client{Key: signer, KID: acme.KeyID(account.KID), DirectoryURL: i.cfg.DirectoryURL, HTTPClient: i.cfg.HTTPClient, RetryBackoff: acmeRetryBackoff}
	return i.client, nil
}

// acmeRetryBackoff bounds the client's internal retries. Transient server
// errors get a few quick retries; a rate limit stops at once so the error
// reaches the manager, which honors Retry-After and schedules the next
// attempt itself instead of sleeping inside this one.
func acmeRetryBackoff(n int, _ *http.Request, res *http.Response) time.Duration {
	if res != nil && res.StatusCode == http.StatusTooManyRequests {
		return 0
	}
	if n > 4 {
		return 0
	}
	if n < 1 {
		n = 1
	}
	return time.Duration(1<<uint(n-1)) * time.Second
}

// accountKeyID reads the account URL the client cached during Register.
func accountKeyID(account *acme.Account, client *acme.Client) string {
	if account != nil && account.URI != "" {
		return account.URI
	}
	return string(client.KID)
}

func parseSigner(keyPEM []byte) (crypto.Signer, error) {
	block, rest := pem.Decode(keyPEM)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("certs: the stored acme account key is not a single PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("certs: parse acme account key: %w", err)
	}
	signer, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, errors.New("certs: the stored acme account key is not a signer")
	}
	return signer, nil
}

func encodeKey(key crypto.Signer) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("certs: encode private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func encodeChain(der [][]byte) []byte {
	var buf bytes.Buffer
	for _, cert := range der {
		_ = pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: cert})
	}
	return buf.Bytes()
}

// classifyACME maps client errors to this package's classes: rate limits
// keep the server's retry delay, cancellations stay cancellations.
func classifyACME(ctx context.Context, stage string, err error) error {
	if retryAfter, limited := acme.RateLimit(err); limited {
		return &RateLimitedError{RetryAfter: retryAfter, Err: fmt.Errorf("acme: %s: %w", stage, err)}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("acme: %s: %w", stage, ctxErr)
	}
	return fmt.Errorf("acme: %s: %w", stage, err)
}

// CNAMEResolver reads CNAME records. *net.Resolver satisfies it; tests use a
// fake so no test ever depends on public DNS.
type CNAMEResolver interface {
	LookupCNAME(ctx context.Context, host string) (string, error)
}

// normalizeDelegations lower-cases and validates the delegation table. The
// target must be a plain host name that is not, and is not under, the name
// the CNAME lives at: a delegation to itself can never resolve.
func normalizeDelegations(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for base, target := range in {
		base = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(base)), ".")
		target = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(target)), ".")
		if base == "" || strings.HasPrefix(base, "*.") || target == "" {
			return nil, errors.New("certs: a challenge delegation needs a domain and a target name")
		}
		for _, name := range []string{base, target} {
			if _, err := wireName(name); err != nil || strings.ContainsAny(name, " *") {
				return nil, fmt.Errorf("certs: challenge delegation name %q is not a valid host name", name)
			}
		}
		source := "_acme-challenge." + base
		if target == source || strings.HasSuffix(target, "."+source) {
			return nil, fmt.Errorf("certs: challenge delegation for %s cannot point at itself", base)
		}
		out[base] = target
	}
	return out, nil
}
