package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

type differentialRequest struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
	Host   string `json:"host"`
	Header string `json:"header"`
	Body   string `json:"body"`
}

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestDifferentialProxyAgainstCaddyOrNginx compares the request semantics of
// Ozal's host-routed reverse proxy with an installed reference proxy.
// It is opt-in through OZAL_DIFFERENTIAL_PROXY=1: hosted CI images ship
// nginx, and the gate must not depend on whatever a runner has installed.
func TestDifferentialProxyAgainstCaddyOrNginx(t *testing.T) {
	if os.Getenv("OZAL_DIFFERENTIAL_PROXY") != "1" {
		t.Skip("set OZAL_DIFFERENTIAL_PROXY=1 and install caddy or nginx to run the differential proxy harness")
	}
	type engine struct {
		name string
		path string
	}
	var engines []engine
	for _, name := range []string{"caddy", "nginx"} {
		if path, err := exec.LookPath(name); err == nil {
			engines = append(engines, engine{name: name, path: path})
		}
	}
	if len(engines) == 0 {
		t.Skip("install caddy or nginx to run the differential proxy harness")
	}

	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(differentialRequest{
			Method: r.Method,
			URI:    r.RequestURI,
			Host:   r.Host,
			Header: r.Header.Get("X-Differential-Input"),
			Body:   string(body),
		})
	}))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for differential upstream: %v", err)
	}
	upstream.Listener = listener
	upstream.Start()
	defer upstream.Close()

	handler, err := New([]config.Route{{Host: "app.example.test", Target: upstream.URL}}, nil, nil)
	if err != nil {
		t.Fatalf("build Ozal proxy: %v", err)
	}
	ozal := httptest.NewServer(handler)
	defer ozal.Close()
	want := sendDifferentialRequest(t, ozal.URL)

	for _, proxy := range engines {
		t.Run(proxy.name, func(t *testing.T) {
			baseURL := startReferenceProxy(t, proxy.name, proxy.path, upstream.URL)
			got := sendDifferentialRequest(t, baseURL)
			if got != want {
				t.Fatalf("forwarded request differs\nOzal: %+v\n%s: %+v", want, proxy.name, got)
			}
		})
	}
}

func sendDifferentialRequest(t *testing.T, proxyURL string) differentialRequest {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, proxyURL+"/docs/chapter-one?filter=active%20users", strings.NewReader("payload=one&payload=two"))
	if err != nil {
		t.Fatalf("create differential request: %v", err)
	}
	req.Host = "app.example.test"
	req.Header.Set("X-Differential-Input", "preserve-this-header")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("send differential request to %s: %v", proxyURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy response status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var got differentialRequest
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode upstream request: %v", err)
	}
	return got
}

func startReferenceProxy(t *testing.T, name, binary, upstreamURL string) string {
	t.Helper()
	upstream, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	upstreamHost, upstreamPort, err := net.SplitHostPort(upstream.Host)
	if err != nil {
		t.Fatalf("parse upstream address: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve reference proxy port: %v", err)
	}
	proxyAddress := listener.Addr().String()
	_, proxyPort, err := net.SplitHostPort(proxyAddress)
	if err != nil {
		listener.Close()
		t.Fatalf("parse reference proxy address: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("release reference proxy port: %v", err)
	}

	dir := t.TempDir()
	var cmd *exec.Cmd
	switch name {
	case "caddy":
		configText := fmt.Sprintf("{\n admin off\n auto_https off\n}\n127.0.0.1:%s {\n reverse_proxy %s {\n  header_up Host {http.request.header.Host}\n }\n}\n", proxyPort, upstream.Host)
		configPath := filepath.Join(dir, "Caddyfile")
		if err := os.WriteFile(configPath, []byte(configText), 0o600); err != nil {
			t.Fatalf("write Caddyfile: %v", err)
		}
		cmd = exec.Command(binary, "run", "--config", configPath, "--adapter", "caddyfile")
	case "nginx":
		configText := fmt.Sprintf("worker_processes 1;\npid %s;\nerror_log stderr;\nevents { worker_connections 32; }\nhttp {\n access_log off;\n server {\n  listen 127.0.0.1:%s;\n  location / {\n   proxy_http_version 1.1;\n   proxy_set_header Connection \"\";\n   proxy_set_header Host $http_host;\n   proxy_pass http://%s:%s;\n  }\n }\n}\n", filepath.Join(dir, "nginx.pid"), proxyPort, upstreamHost, upstreamPort)
		configPath := filepath.Join(dir, "nginx.conf")
		if err := os.WriteFile(configPath, []byte(configText), 0o600); err != nil {
			t.Fatalf("write nginx config: %v", err)
		}
		cmd = exec.Command(binary, "-p", dir+string(os.PathSeparator), "-c", configPath, "-g", "daemon off;")
	default:
		t.Fatalf("unsupported reference proxy %q", name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	output := &synchronizedBuffer{}
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmd.Stdout, cmd.Stderr = output, output
	// nginx's master forks a worker that inherits the output pipe. Killing only
	// the master left Wait blocked on that pipe until the test binary timed
	// out. An interrupt is a fast shutdown that stops the workers too, and
	// WaitDelay bounds both the kill and the wait for the pipe to close.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	done := make(chan struct{})
	var processErr error
	go func() {
		processErr = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("%s exited before listening: %v\n%s", name, processErr, output.String())
		default:
		}
		conn, err := net.DialTimeout("tcp", proxyAddress, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return "http://" + proxyAddress
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s did not listen on %s\n%s", name, proxyAddress, output.String())
	return ""
}
