package admin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

// localRequest builds a request as LocalConn marks one from the unix socket.
func localRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Host = "localhost"
	return req.WithContext(context.WithValue(req.Context(), localKey{}, true))
}

func TestLocalConnMarksOnlyUnixConnections(t *testing.T) {
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "admin.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		if c, err := net.Dial("unix", listener.Addr().String()); err == nil {
			defer c.Close()
		}
	}()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if !isLocal(req.WithContext(LocalConn(context.Background(), conn))) {
		t.Fatal("unix connection not marked local")
	}

	tcp, peer := net.Pipe() // not a *net.UnixConn
	defer tcp.Close()
	defer peer.Close()
	if isLocal(req.WithContext(LocalConn(context.Background(), tcp))) {
		t.Fatal("non-unix connection marked local")
	}
}

func TestBearerAdmitsLocalRequestWithoutToken(t *testing.T) {
	// No admin.token at all: TCP mutations are disabled, the socket is not.
	h := New(&fakeManager{}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, localRequest(http.MethodPost, "/_/reload"))
	if rec.Code != http.StatusOK {
		t.Fatalf("local reload = %d, want 200: %s", rec.Code, rec.Body)
	}
}

func TestRequireLocalRejectsTCPEvenWithAdminToken(t *testing.T) {
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}, nil)
	called := false
	guarded := h.requireLocal(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodPost, "/_/api/people/invite", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	guarded(rec, req)

	if called {
		t.Fatal("requireLocal called next for a TCP request")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("body = %q, want a JSON error", rec.Body)
	}
}

func TestRequireLocalAdmitsSocketRequest(t *testing.T) {
	h := New(&fakeManager{}, nil)
	called := false
	guarded := h.requireLocal(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	guarded(rec, localRequest(http.MethodPost, "/_/api/people/invite"))
	if !called || rec.Code != http.StatusNoContent {
		t.Fatalf("called=%v status=%d, want next to run", called, rec.Code)
	}
}
