package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/switchboard-project/switchboard/internal/audit"
	"github.com/switchboard-project/switchboard/internal/auth"
	"github.com/switchboard-project/switchboard/internal/config"
)

type benchmarkRoundTripper struct{}

func (benchmarkRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    r,
	}, nil
}

type benchmarkGate struct{ principal auth.Principal }

func (g benchmarkGate) Authenticate(*http.Request) (auth.Principal, bool) {
	return g.principal, true
}
func (benchmarkGate) RedirectToLogin(http.ResponseWriter, *http.Request) {}

type benchmarkResponseWriter struct {
	header http.Header
	status int
}

func (w *benchmarkResponseWriter) Header() http.Header { return w.header }
func (w *benchmarkResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *benchmarkResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return len(body), nil
}
func (w *benchmarkResponseWriter) reset() {
	clear(w.header)
	w.status = 0
}

func BenchmarkProxyPublicRoute(b *testing.B) {
	benchmarkProxy(b, config.Route{Host: "app.localhost", Target: "http://upstream.invalid"}, nil)
}

func BenchmarkProxyProtectedRoute(b *testing.B) {
	policy := &config.AccessPolicy{AllowedEmails: []string{"alice@example.com"}}
	principal := auth.Principal{Email: "Alice@Example.com", EmailVerified: true}
	benchmarkProxy(b, config.Route{Host: "app.localhost", Target: "http://upstream.invalid", Access: policy}, benchmarkGate{principal: principal})
}

func BenchmarkProxyProtectedRouteRequiredAudit(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("durable audit requires owner-only POSIX file modes")
	}
	directory, err := os.MkdirTemp(".", ".audit-benchmark-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(directory) })
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		b.Fatal(err)
	}
	writer, err := audit.Open(b.Context(), absoluteDirectory, audit.Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := writer.Close(); err != nil {
			b.Error(err)
		}
	})
	policy := &config.AccessPolicy{AllowedEmails: []string{"alice@example.com"}}
	principal := auth.Principal{Email: "Alice@Example.com", EmailVerified: true}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := New(
		[]config.Route{{Host: "app.localhost", Target: "http://upstream.invalid", Access: policy}},
		nil,
		benchmarkGate{principal: principal},
		WithTransport(benchmarkRoundTripper{}),
		WithLogger(logger),
		WithAuditAppender(writer),
	)
	if err != nil {
		b.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://app.localhost/health", nil)
	request.Host = "app.localhost"
	request.RemoteAddr = "192.0.2.10:12345"
	response := &benchmarkResponseWriter{header: make(http.Header)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response.reset()
		handler.ServeHTTP(response, request)
	}
	b.StopTimer()
	if response.status != http.StatusOK {
		b.Fatalf("benchmark response status = %d, want %d", response.status, http.StatusOK)
	}
}

func BenchmarkProxyProtectedRouteRequiredAuditParallel(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("durable audit requires owner-only POSIX file modes")
	}
	directory, err := os.MkdirTemp(".", ".audit-benchmark-parallel-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(directory) })
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		b.Fatal(err)
	}
	writer, err := audit.Open(b.Context(), absoluteDirectory, audit.Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := writer.Close(); err != nil {
			b.Error(err)
		}
	})
	policy := &config.AccessPolicy{AllowedEmails: []string{"alice@example.com"}}
	principal := auth.Principal{Email: "Alice@Example.com", EmailVerified: true}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := New(
		[]config.Route{{Host: "app.localhost", Target: "http://upstream.invalid", Access: policy}},
		nil,
		benchmarkGate{principal: principal},
		WithTransport(benchmarkRoundTripper{}),
		WithLogger(logger),
		WithAuditAppender(writer),
	)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		request := httptest.NewRequest(http.MethodGet, "http://app.localhost/health", nil)
		request.Host = "app.localhost"
		request.RemoteAddr = "192.0.2.10:12345"
		response := &benchmarkResponseWriter{header: make(http.Header)}
		for pb.Next() {
			response.reset()
			handler.ServeHTTP(response, request)
			if response.status != http.StatusOK {
				b.Errorf("benchmark response status = %d, want %d", response.status, http.StatusOK)
				return
			}
		}
	})
}

func benchmarkProxy(b *testing.B, route config.Route, gate AuthGate) {
	b.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := New([]config.Route{route}, nil, gate, WithTransport(benchmarkRoundTripper{}), WithLogger(logger))
	if err != nil {
		b.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://app.localhost/health", nil)
	request.Host = "app.localhost"
	request.RemoteAddr = "192.0.2.10:12345"
	writer := &benchmarkResponseWriter{header: make(http.Header)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		writer.reset()
		handler.ServeHTTP(writer, request)
	}
	if writer.status != http.StatusOK {
		b.Fatalf("benchmark response status = %d, want %d", writer.status, http.StatusOK)
	}
}
