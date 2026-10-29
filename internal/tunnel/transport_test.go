package tunnel

import (
	"bufio"
	"context"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/proxy"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConfiguredTransportCapacityIsHeldUntilBodyClose(t *testing.T) {
	lab := newEnrolledLab(t)
	runLoop(t, lab, lab.enroll(t, "portfolio"), nil, "ok")
	waitFor(t, hasConnector(lab, "portfolio"))
	transport := lab.current().TransportFor("portfolio")
	var responses []*http.Response
	defer func() {
		for _, response := range responses {
			response.Body.Close()
		}
	}()
	request := func() (*http.Response, error) {
		req, err := http.NewRequest(http.MethodGet, "http://tunnel.invalid/", nil)
		if err != nil {
			t.Fatal(err)
		}
		return transport.RoundTrip(req)
	}
	for i := 0; i < cap(lab.current().requestSlots); i++ {
		response, err := request()
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if response, err := request(); err == nil {
		response.Body.Close()
		t.Fatal("unbounded tunnel requests accepted")
	}
	responses[0].Body.Close()
	responses[0].Body.Close() // Never release another request's slot.
	response, err := request()
	if err != nil {
		t.Fatal(err)
	}
	responses = append(responses, response)
	if response, err := request(); err == nil {
		response.Body.Close()
		t.Fatal("double close released another request's slot")
	}
}

func TestConfiguredTransportRejectsRevokedAndOfflineConnectors(t *testing.T) {
	lab := newEnrolledLab(t)
	runLoop(t, lab, lab.enroll(t, "portfolio"), nil, "ok")
	waitFor(t, hasConnector(lab, "portfolio"))
	transport := lab.current().TransportFor("portfolio")
	req, _ := http.NewRequest(http.MethodGet, "http://tunnel.invalid/", nil)
	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err := lab.reg.Revoke(context.Background(), "portfolio"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !lab.current().ConnectorConnected("portfolio") })
	if response, err := transport.RoundTrip(req); err == nil {
		response.Body.Close()
		t.Fatal("revoked connector forwarded a request")
	}
	if len(lab.current().requestSlots) != 0 {
		t.Fatal("failed round trip leaked a capacity slot")
	}
}

func TestConfiguredTransportPreservesBidirectionalUpgrade(t *testing.T) {
	lab := newEnrolledLab(t)
	cfg := lab.enroll(t, "portfolio")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, err = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
		if err != nil {
			return
		}
		if err := buffer.Flush(); err != nil {
			return
		}
		_, _ = io.Copy(conn, buffer)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Connect(ctx, lab.addr, cfg, handler) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("connector did not stop")
		}
	}()
	waitFor(t, hasConnector(lab, "portfolio"))
	edgeHandler, err := proxy.New([]config.Route{{Host: "app.localhost", Connector: "portfolio"}}, nil, nil, proxy.WithConnectorTransports(map[string]http.RoundTripper{"portfolio": lab.current().TransportFor("portfolio")}))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(edgeHandler)
	defer server.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: app.localhost\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status: %d", response.StatusCode)
	}
	if _, err := io.WriteString(conn, "bidirectional echo\n"); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadString('\n')
	if err != nil || line != "bidirectional echo\n" {
		t.Fatalf("upgrade echo %q: %v", line, err)
	}
	conn.Close()
	waitFor(t, func() bool { return len(lab.current().requestSlots) == 0 })
}
