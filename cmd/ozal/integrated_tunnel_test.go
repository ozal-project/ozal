package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/admin"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/proxy"
	"github.com/ozal-project/ozal/internal/routemanager"
	"github.com/ozal-project/ozal/internal/tunnel"
)

func TestIntegratedTunnelRoutesReloadAndPreserveVisitorIP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	directory := t.TempDir()
	pki := filepath.Join(directory, "pki")
	if err := tunnel.InitLab(pki); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(directory, "state.db")
	if err := runMigrate(ctx, []string{"-db", dbPath}, io.Discard); err != nil {
		t.Fatal(err)
	}
	db, err := openMigratedDB(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry := tunnel.NewRegistry(db)
	fp, err := fingerprintFromPEM(filepath.Join(pki, "connector.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Enroll(ctx, "portfolio", fp); err != nil {
		t.Fatal(err)
	}
	if err := registry.Enroll(ctx, "offline", tunnel.FingerprintOf([]byte("offline fixture"))); err != nil {
		t.Fatal(err)
	}
	tunnelConfig := &config.Tunnel{Listen: "127.0.0.1:0", CertFile: filepath.Join(pki, "edge.crt"), KeyFile: filepath.Join(pki, "edge.key"), CAFile: filepath.Join(pki, "ca.crt")}
	recorder := &events.Recorder{}
	broker := events.NewBroker()
	sink := events.Multi{recorder, broker}
	integrated, err := startIntegratedTunnel(ctx, tunnelConfig, db, sink)
	if err != nil {
		t.Fatal(err)
	}
	defer integrated.Close()
	cfg := config.Config{Listen: "127.0.0.1:0", Admin: config.Admin{Token: "console-read-token"}, Tunnel: tunnelConfig, TrustedProxies: []string{"127.0.0.1/32"}, Routes: []config.Route{{Host: "app.localhost", Connector: "portfolio"}}}
	configPath := filepath.Join(directory, "routing.json")
	write := func() {
		t.Helper()
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	manager, err := routemanager.New(ctx, configPath, time.Hour, routemanager.WithConnectorRoutes(integrated), routemanager.WithEventSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(manager)
	defer public.Close()
	var requests atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"ip": r.Header.Get("X-Forwarded-For"), "internal": r.Header.Get("X-Switchboard-Visitor-IP"), "identity": r.Header.Get("X-Auth-Request-Email"), "cookie": r.Header.Get("Cookie")})
	}))
	defer origin.Close()
	connectorHandler, err := proxy.New([]config.Route{{Host: "app.localhost", Target: origin.URL}}, nil, nil, proxy.WithAuthenticatedTunnelTrust())
	if err != nil {
		t.Fatal(err)
	}
	cert, roots, err := tunnel.LoadIdentity(filepath.Join(pki, "connector.crt"), filepath.Join(pki, "connector.key"), filepath.Join(pki, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	connectorTLS, err := tunnel.ClientTLS(cert, roots, "edge.localhost")
	if err != nil {
		t.Fatal(err)
	}
	connectCtx, disconnect := context.WithCancel(ctx)
	connectorDone := make(chan error, 1)
	go func() {
		connectorDone <- tunnel.Connect(connectCtx, integrated.address, connectorTLS, connectorHandler)
	}()
	defer func() {
		disconnect()
		select {
		case <-connectorDone:
		case <-time.After(5 * time.Second):
			t.Error("connector did not stop")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !integrated.ConnectorConnected("portfolio") {
		if time.Now().After(deadline) {
			t.Fatal("connector did not attach")
		}
		time.Sleep(5 * time.Millisecond)
	}
	do := func() (*http.Response, map[string]string) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, public.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "app.localhost"
		request.Header.Set("CF-Connecting-IP", "203.0.113.47")
		request.Header.Set("X-Switchboard-Visitor-IP", "6.6.6.6")
		request.Header.Set("X-Auth-Request-Email", "forged@example.com")
		request.Header.Set("Cookie", "sb_session=forged; harmless=kept")
		response, err := public.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		values := map[string]string{}
		if response.StatusCode == http.StatusOK {
			if err := json.NewDecoder(response.Body).Decode(&values); err != nil {
				t.Fatal(err)
			}
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
		return response, values
	}
	response, values := do()
	if response.StatusCode != http.StatusOK || values["ip"] != "203.0.113.47" || values["internal"] != "" || values["identity"] != "" || values["cookie"] != "harmless=kept" {
		t.Fatalf("tunnel response %d: %+v", response.StatusCode, values)
	}
	if !recorder.Has(tunnel.TunnelConnected) {
		t.Fatal("connect event missing from common sink")
	}
	// Reload changes only the route mapping, with the established session retained.
	cfg.Routes[0].Connector = "offline"
	write()
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if response, _ := do(); response.StatusCode != http.StatusBadGateway {
		t.Fatalf("offline route: %d", response.StatusCode)
	}
	if requests.Load() != 1 {
		t.Fatal("offline connector fell back to the HTTP origin")
	}
	cfg.Routes[0].Connector = "portfolio"
	write()
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if response, _ := do(); response.StatusCode != http.StatusOK {
		t.Fatalf("restored route: %d", response.StatusCode)
	}
	// Unenrolled and listener changes reject the whole candidate.
	cfg.Routes[0].Connector = "unknown"
	write()
	if err := manager.Reload(); err == nil {
		t.Fatal("unenrolled connector accepted")
	}
	if response, _ := do(); response.StatusCode != http.StatusOK {
		t.Fatal("failed reload replaced working mapping")
	}
	cfg.Routes[0].Connector = "portfolio"
	cfg.Tunnel.Listen = "127.0.0.1:12345"
	write()
	if err := manager.Reload(); err == nil {
		t.Fatal("transport-listener change accepted without restart")
	}
	control := admin.New(manager, sink, admin.WithEvents(broker))
	status := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_/status", nil)
	request.Header.Set("Authorization", "Bearer console-read-token")
	control.ServeHTTP(status, request)
	if !strings.Contains(status.Body.String(), "Connector connected") || strings.Contains(status.Body.String(), "upstream responses are healthy") {
		t.Fatal("console lacks connector state")
	}
	adminServer := httptest.NewServer(control)
	defer adminServer.Close()
	streamClient := &http.Client{Timeout: 2 * time.Second}
	streamRequest, err := http.NewRequest(http.MethodGet, adminServer.URL+"/_/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	streamRequest.Header.Set("Authorization", "Bearer console-read-token")
	stream, err := streamClient.Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stream.Body)
	found := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "tunnel.connected") {
			found = true
			break
		}
	}
	// Scan also stops on a read error (the 2s client timeout included); report
	// it instead of the misleading "event missing".
	if err := scanner.Err(); err != nil {
		t.Fatalf("live console stream: %v", err)
	}
	stream.Body.Close()
	if !found {
		t.Fatal("tunnel event missing from live console stream")
	}
	disconnect()
	deadline = time.Now().Add(5 * time.Second)
	for integrated.ConnectorConnected("portfolio") {
		if time.Now().After(deadline) {
			t.Fatal("disconnected connector stayed live")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if response, _ := do(); response.StatusCode != http.StatusBadGateway {
		t.Fatalf("disconnected route: %d", response.StatusCode)
	}
	if !recorder.Has(tunnel.TunnelDisconnected) {
		t.Fatal("disconnect event missing")
	}
}

func TestIntegratedTunnelRequiresState(t *testing.T) {
	if _, err := startIntegratedTunnel(context.Background(), &config.Tunnel{}, nil, nil); err == nil {
		t.Fatal("tunnel started without migrated registry")
	}
}

// One connector identity can serve several hosts: the edge maps each host to
// the same enrolled connector, and the connector answers each from its own
// fixed route. Hosts the connector was not started with are never served.
func TestOneConnectorServesSeveralHosts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	directory := t.TempDir()
	pki := filepath.Join(directory, "pki")
	if err := tunnel.InitLab(pki); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(directory, "state.db")
	if err := runMigrate(ctx, []string{"-db", dbPath}, io.Discard); err != nil {
		t.Fatal(err)
	}
	db, err := openMigratedDB(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fp, err := fingerprintFromPEM(filepath.Join(pki, "connector.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tunnel.NewRegistry(db).Enroll(ctx, "portfolio", fp); err != nil {
		t.Fatal(err)
	}

	site := filepath.Join(directory, "site")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("static site"), 0o644); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "api answer")
	}))
	defer api.Close()

	tunnelConfig := &config.Tunnel{Listen: "127.0.0.1:0", CertFile: filepath.Join(pki, "edge.crt"), KeyFile: filepath.Join(pki, "edge.key"), CAFile: filepath.Join(pki, "ca.crt")}
	sink := events.Multi{&events.Recorder{}}
	integrated, err := startIntegratedTunnel(ctx, tunnelConfig, db, sink)
	if err != nil {
		t.Fatal(err)
	}
	defer integrated.Close()
	cfg := config.Config{Listen: "127.0.0.1:0", Tunnel: tunnelConfig, TrustedProxies: []string{"127.0.0.1/32"}, Routes: []config.Route{
		{Host: "site.localhost", Connector: "portfolio"},
		{Host: "api.localhost", Connector: "portfolio"},
		{Host: "unserved.localhost", Connector: "portfolio"},
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "routing.json")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := routemanager.New(ctx, configPath, time.Hour, routemanager.WithConnectorRoutes(integrated), routemanager.WithEventSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(manager)
	defer public.Close()

	routes, err := connectorRoutes("site.localhost", site, "", []string{"api.localhost=upstream:" + api.URL})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := proxy.New(routes, nil, nil, proxy.WithAuthenticatedTunnelTrust())
	if err != nil {
		t.Fatal(err)
	}
	cert, roots, err := tunnel.LoadIdentity(filepath.Join(pki, "connector.crt"), filepath.Join(pki, "connector.key"), filepath.Join(pki, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	clientTLS, err := tunnel.ClientTLS(cert, roots, "edge.localhost")
	if err != nil {
		t.Fatal(err)
	}
	connectCtx, disconnect := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- tunnel.Connect(connectCtx, integrated.address, clientTLS, handler) }()
	defer func() {
		disconnect()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("connector did not stop")
		}
	}()
	for deadline := time.Now().Add(5 * time.Second); !integrated.ConnectorConnected("portfolio"); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("connector did not attach")
		}
	}

	fetch := func(host string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, public.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := public.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if code, body := fetch("site.localhost"); code != http.StatusOK || body != "static site" {
		t.Errorf("site host: %d %q", code, body)
	}
	if code, body := fetch("api.localhost"); code != http.StatusOK || body != "api answer" {
		t.Errorf("api host: %d %q", code, body)
	}
	if code, _ := fetch("unserved.localhost"); code == http.StatusOK {
		t.Errorf("a host the connector was not started with was served (%d)", code)
	}
}
