package probe

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/domaincheck"
)

func TestTokensAnswerOnlyTheirHostUntilTheyExpire(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tokens := NewTokens(func() time.Time { return now })
	token, err := tokens.Issue("testing.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !tokens.Answers("TESTING.example.com", token) {
		t.Fatal("the issued host was refused")
	}
	if tokens.Answers("other.example.com", token) || tokens.Answers("testing.example.com", "guess") {
		t.Fatal("a token answered for another host or a guess")
	}
	now = now.Add(tokenTTL)
	if tokens.Answers("testing.example.com", token) {
		t.Fatal("an expired token still answered")
	}
	for i := 0; i < maxTokens*2; i++ {
		if _, err := tokens.Issue("flood.example.com"); err != nil {
			t.Fatal(err)
		}
	}
	if len(tokens.issued) > maxTokens {
		t.Fatalf("registry grew to %d", len(tokens.issued))
	}
}

func TestHandlerAnswersALiveTokenAndPassesEverythingElse(t *testing.T) {
	tokens := NewTokens(nil)
	token, _ := tokens.Issue("testing.example.com")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "application") })
	handler := Handler(tokens, next)
	for _, tc := range []struct{ host, path, want string }{
		{"testing.example.com", Path + token, token},
		{"other.example.com", Path + token, "application"},
		{"testing.example.com", Path + "unknown", "application"},
		{"testing.example.com", "/", "application"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+tc.path, nil)
		handler.ServeHTTP(rec, req)
		if rec.Body.String() != tc.want {
			t.Errorf("%s%s answered %q, want %q", tc.host, tc.path, rec.Body.String(), tc.want)
		}
	}
}

type fakeResolver struct {
	addrs []netip.Addr
	err   error
}

func (f fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return f.addrs, f.err
}
func (f fakeResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	return host + ".", nil
}

// prober sends every request to server whatever the hostname resolves to,
// as a real client reaching that address would.
func prober(tokens *Tokens, resolver Resolver, server *httptest.Server) *Prober {
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	address := strings.TrimPrefix(strings.TrimPrefix(server.URL, "https://"), "http://")
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	client.Transport = transport
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Prober{Resolver: resolver, Client: client, Tokens: tokens}
}

func TestProberClassifiesWhatAnswers(t *testing.T) {
	addrs := fakeResolver{addrs: []netip.Addr{netip.MustParseAddr("203.0.113.10")}}
	tokens := NewTokens(nil)

	edge := httptest.NewServer(Handler(tokens, http.NotFoundHandler()))
	defer edge.Close()
	if o := prober(tokens, addrs, edge).Observe(context.Background(), "testing.example.com", false, nil); o.Reach != domaincheck.ReachEdge || o.Lookup != domaincheck.LookupFound {
		t.Fatalf("this edge: %+v", o)
	}

	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "old hosting") }))
	defer old.Close()
	if o := prober(tokens, addrs, old).Observe(context.Background(), "testing.example.com", false, nil); o.Reach != domaincheck.ReachOther {
		t.Fatalf("another server: %+v", o)
	}

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "cloudflare")
		w.WriteHeader(526)
	}))
	defer proxy.Close()
	proxies := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	if o := prober(tokens, addrs, proxy).Observe(context.Background(), "testing.example.com", false, proxies); o.Reach != domaincheck.ReachProxyOrigin || !o.ViaProxy || o.Status != 526 {
		t.Fatalf("proxy refused by origin: %+v", o)
	}

	untrusted := httptest.NewTLSServer(Handler(tokens, http.NotFoundHandler()))
	defer untrusted.Close()
	p := prober(tokens, addrs, untrusted)
	p.Client.Transport.(*http.Transport).TLSClientConfig = nil // system roots: the test certificate does not verify
	if o := p.Observe(context.Background(), "testing.example.com", true, nil); o.Reach != domaincheck.ReachCertificate {
		t.Fatalf("certificate not valid yet: %+v", o)
	}

	missing := fakeResolver{err: &net.DNSError{Err: "no such host", Name: "testing.example.com", IsNotFound: true}}
	if o := prober(tokens, missing, edge).Observe(context.Background(), "testing.example.com", false, nil); o.Lookup != domaincheck.LookupNotFound || o.Reach != domaincheck.ReachNone {
		t.Fatalf("no record: %+v", o)
	}
}
