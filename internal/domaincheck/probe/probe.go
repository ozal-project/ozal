// Package probe observes whether a hostname reaches this edge: it resolves
// the name, then requests a one-time token over the public internet that
// only this edge's public listener answers. It is the adapter for the pure
// domaincheck core.
package probe

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/ozal-project/ozal/internal/domaincheck"
)

// Path is the prefix the public listener answers before any routing.
const Path = "/.well-known/ozal-probe/"

const (
	tokenTTL  = 2 * time.Minute
	maxTokens = 128
)

// Tokens holds the one-time tokens of checks in flight. A token answers
// only for the hostname it was issued for and only until it expires.
type Tokens struct {
	mu     sync.Mutex
	now    func() time.Time
	issued map[string]issued
}

type issued struct {
	host    string
	expires time.Time
}

// NewTokens returns an empty registry; now is the clock (nil: time.Now).
func NewTokens(now func() time.Time) *Tokens {
	if now == nil {
		now = time.Now
	}
	return &Tokens{now: now, issued: map[string]issued{}}
}

// Issue returns a fresh token for host. When the registry is full the
// soonest-expiring token is dropped: a flood of checks cannot grow memory.
func (t *Tokens) Issue(host string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for k, v := range t.issued {
		if !now.Before(v.expires) {
			delete(t.issued, k)
		}
	}
	if len(t.issued) >= maxTokens {
		oldest := ""
		for k, v := range t.issued {
			if oldest == "" || v.expires.Before(t.issued[oldest].expires) {
				oldest = k
			}
		}
		delete(t.issued, oldest)
	}
	t.issued[token] = issued{host: strings.ToLower(host), expires: now.Add(tokenTTL)}
	return token, nil
}

// Answers reports whether token is live and was issued for host.
func (t *Tokens) Answers(host, token string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.issued[token]
	if !ok || !t.now().Before(v.expires) {
		return false
	}
	host = strings.ToLower(host)
	return v.host == host || domaincheck.Hostname(v.host) == host
}

// Handler answers a live token with itself and passes every other request
// on untouched, so an application's own paths are never shadowed. It sits
// in front of the request pipeline, like an ACME challenge: the answer
// reveals nothing but the token the caller already knows.
func Handler(tokens *Tokens, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.URL.Path, Path)
		if ok && r.Method == http.MethodGet && tokens.Answers(r.Host, token) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = io.WriteString(w, token)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Resolver is the part of net.Resolver the prober uses.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
}

// Prober observes one hostname.
type Prober struct {
	Resolver Resolver
	Client   *http.Client
	Tokens   *Tokens
}

// NewProber uses the system resolver and a client that never follows
// redirects (a redirect is somebody else answering) and gives up after ten
// seconds.
func NewProber(tokens *Tokens) *Prober {
	return &Prober{
		Resolver: net.DefaultResolver,
		Tokens:   tokens,
		Client: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Observe resolves host and requests a fresh token from it. proxies are the
// trusted proxy ranges: addresses inside them are the provider's, not ours.
func (p *Prober) Observe(ctx context.Context, host string, https bool, proxies []netip.Prefix) domaincheck.Observation {
	var o domaincheck.Observation
	name := domaincheck.Hostname(host)
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := p.Resolver.LookupNetIP(lookupCtx, "ip", name)
	var dnsErr *net.DNSError
	switch {
	case err == nil && len(addrs) > 0:
		o.Lookup = domaincheck.LookupFound
		o.Addresses = addrs
		if cname, err := p.Resolver.LookupCNAME(lookupCtx, name); err == nil && !strings.EqualFold(strings.TrimSuffix(cname, "."), name) {
			o.CNAME = strings.TrimSuffix(cname, ".")
		}
		o.ViaProxy = len(proxies) > 0
		for _, a := range addrs {
			if !inAny(a.Unmap(), proxies) {
				o.ViaProxy = false
			}
		}
	case err == nil || errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		o.Lookup = domaincheck.LookupNotFound
		o.Reach = domaincheck.ReachNone
		return o
	default:
		o.Lookup = domaincheck.LookupFailed
		o.Reach = domaincheck.ReachNone
		return o
	}
	o.Reach, o.Status = p.request(ctx, host, https)
	return o
}

func (p *Prober) request(ctx context.Context, host string, https bool) (domaincheck.Reach, int) {
	token, err := p.Tokens.Issue(host)
	if err != nil {
		return domaincheck.ReachNone, 0
	}
	scheme := "http"
	if https {
		scheme = "https"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+host+Path+token, nil)
	if err != nil {
		return domaincheck.ReachNone, 0
	}
	res, err := p.Client.Do(req)
	if err != nil {
		if certificateError(err) {
			return domaincheck.ReachCertificate, 0
		}
		return domaincheck.ReachNone, 0
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 256))
	switch {
	case res.StatusCode == http.StatusOK && string(body) == token:
		return domaincheck.ReachEdge, res.StatusCode
	case res.StatusCode >= 520 && res.StatusCode <= 530 && strings.Contains(strings.ToLower(res.Header.Get("Server")), "cloudflare"):
		return domaincheck.ReachProxyOrigin, res.StatusCode
	default:
		return domaincheck.ReachOther, res.StatusCode
	}
}

func certificateError(err error) bool {
	var verification *tls.CertificateVerificationError
	var hostname x509.HostnameError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &verification) || errors.As(err, &hostname) || errors.As(err, &unknown) || errors.As(err, &invalid)
}

func inAny(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
