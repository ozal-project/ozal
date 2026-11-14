package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/cftunnel"
	"github.com/ozal-project/ozal/internal/cloudflare"
	"github.com/ozal-project/ozal/internal/config"
)

type ingressFake struct {
	applies  int
	applyErr error
}

func (f *ingressFake) Inspect(context.Context, config.Config) cftunnel.Report {
	return cftunnel.Report{Mode: config.IngressCloudflareTunnel, Name: "ozal", Steps: []cloudflare.TunnelStep{{Name: cloudflare.TunnelExists, State: cloudflare.TunnelTodo, Action: cloudflare.CreateTunnel}}}
}

func (f *ingressFake) Apply(context.Context, config.Config) (cftunnel.Report, error) {
	f.applies++
	return cftunnel.Report{Mode: config.IngressCloudflareTunnel, Name: "ozal", ID: "c0ffee00", Applied: []string{"create_tunnel"}}, f.applyErr
}

func TestIngressEndpoints(t *testing.T) {
	direct := &fakeManager{cfg: config.Config{Listen: ":8888", Admin: config.Admin{Token: readToken}}}
	service := &ingressFake{}
	handler := New(direct, nil, WithIngress(service))
	if rec := request(handler, http.MethodGet, "/_/api/ingress", readToken, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"mode":"direct"`) {
		t.Fatalf("direct GET = %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodPost, "/_/api/ingress/cloudflare-tunnel", readToken, ""); rec.Code != http.StatusConflict || service.applies != 0 {
		t.Fatalf("direct POST = %d, applies %d", rec.Code, service.applies)
	}

	cfg, err := config.Validate(config.Config{
		Listen: "127.0.0.1:8888", Admin: config.Admin{Token: readToken},
		Auth:    config.Auth{Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com"}},
		Ingress: &config.Ingress{Mode: config.IngressCloudflareTunnel, CloudflareTunnel: &config.CloudflareTunnelIngress{Token: "env:CF_TOKEN"}},
		Routes:  []config.Route{{Host: "notes.example.com", Target: "http://127.0.0.1:3000"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler = New(&fakeManager{cfg: *cfg}, nil, WithIngress(service))
	if rec := request(handler, http.MethodGet, "/_/api/ingress", "", ""); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated GET = %d", rec.Code)
	}
	if rec := request(handler, http.MethodGet, "/_/api/ingress", readToken, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"create_tunnel"`) {
		t.Fatalf("tunnel GET = %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodPost, "/_/api/ingress/cloudflare-tunnel", readToken, ""); rec.Code != http.StatusOK || service.applies != 1 || !strings.Contains(rec.Body.String(), `"c0ffee00"`) {
		t.Fatalf("tunnel POST = %d %s", rec.Code, rec.Body)
	}
	service.applyErr = errors.New("creating tunnel ozal failed: API error 403")
	if rec := request(handler, http.MethodPost, "/_/api/ingress/cloudflare-tunnel", readToken, ""); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "403") {
		t.Fatalf("failed POST = %d %s", rec.Code, rec.Body)
	}
}
