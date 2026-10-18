package proxy

import (
	"github.com/octaviosantacruz/switchboard/internal/config"
	"net/http"
	"testing"
)

func TestConnectorRouteRequiresExplicitTransport(t *testing.T) {
	routes := []config.Route{{Host: "app.localhost", Connector: "portfolio"}}
	if _, err := New(routes, nil, nil); err == nil {
		t.Fatal("connector route accepted with TCP fallback")
	}
	for _, route := range []config.Route{{Host: "app.localhost", Connector: "portfolio", Target: "http://localhost:8000"}, {Host: "app.localhost", Connector: "portfolio", Access: &config.AccessPolicy{}}} {
		if _, err := New([]config.Route{route}, nil, nil, WithConnectorTransports(map[string]http.RoundTripper{"portfolio": http.DefaultTransport})); err == nil {
			t.Fatal("ambiguous or unsupported protected connector route accepted")
		}
	}
}
