package admin

import (
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/routemanager"
	"net/http"
	"strings"
	"testing"
)

func TestConnectorStatusProjectionAndEscaping(t *testing.T) {
	route := config.Route{Host: "app.localhost", Connector: "<script>"}
	manager := &fakeManager{cfg: config.Config{Routes: []config.Route{route}}, statuses: []routemanager.RouteStatus{{Route: route, ConnectorConnected: true}}}
	handler := New(manager, nil, WithEvents(events.NewBroker()))
	overview := request(handler, http.MethodGet, "/_/overview", "", "")
	if !strings.Contains(overview.Body.String(), "Connector connected") || !strings.Contains(overview.Body.String(), `\u003cscript\u003e`) {
		t.Fatalf("unsafe overview: %s", overview.Body)
	}
	status := request(handler, http.MethodGet, "/_/status", "", "")
	if strings.Contains(status.Body.String(), "<script>") || !strings.Contains(status.Body.String(), "connector:&lt;script&gt;") || !strings.Contains(status.Body.String(), "does not prove application functionality") {
		t.Fatal("connector state missing or unsafe HTML")
	}
	manager.statuses[0].ConnectorConnected = false
	status = request(handler, http.MethodGet, "/_/status", "", "")
	if !strings.Contains(status.Body.String(), "Connector offline") {
		t.Fatal("offline connector labeled healthy")
	}
}
