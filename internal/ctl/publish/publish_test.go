package publish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/ctl"
)

// fakeEdge is the admin API (sites token only) and the public site it serves.
type fakeEdge struct {
	mu        sync.Mutex
	live      map[string]string
	content   map[string][]byte // deployment id -> index.html
	uploads   int
	rollbacks []string
	serve     func(live []byte) []byte // what the public answer serves
}

func (f *fakeEdge) admin(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sites-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/_/api/sites":
			sites := []map[string]string{}
			for name, live := range f.live {
				sites = append(sites, map[string]string{"name": name, "live": live})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"sites": sites})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/deployments"):
			body, _ := io.ReadAll(r.Body)
			if r.Header.Get("Content-Type") != "application/gzip" || len(body) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.uploads++
			id := "d" + string(rune('0'+f.uploads))
			f.live["blog"] = id
			f.content[id] = []byte("<h1>new build</h1>")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rollback"):
			var req struct{ ID string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.rollbacks = append(f.rollbacks, req.ID)
			f.live["blog"] = req.ID
			_ = json.NewEncoder(w).Encode(map[string]string{"id": req.ID})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func (f *fakeEdge) public() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		page := f.content[f.live["blog"]]
		f.mu.Unlock()
		if r.URL.Path != "/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if f.serve != nil {
			page = f.serve(page)
		}
		_, _ = w.Write(page)
	}))
}

type fakeTunnel struct {
	addr    string
	opened  int
	stopped int
}

func (t *fakeTunnel) Forward(_ context.Context, remote string) (string, func(), error) {
	t.opened++
	return t.addr, func() { t.stopped++ }, nil
}

func build(t *testing.T) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>new build</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func request(t *testing.T, edge *fakeEdge, apply bool) (Request, *fakeTunnel, func()) {
	admin, public := edge.admin(t), edge.public()
	target := ctl.Target{Edge: ctl.Edge{Admin: "127.0.0.1:9091"}}
	tunnel := &fakeTunnel{addr: strings.TrimPrefix(admin.URL, "http://")}
	return Request{Target: target, Site: "blog", Dir: build(t), Host: "blog.example.com", Token: "sites-token", Apply: apply,
			PublicURL: public.URL, Admin: admin.Client(), Public: public.Client(), Retries: 2, Wait: 10 * time.Millisecond},
		tunnel, func() { admin.Close(); public.Close() }
}

func TestPublishUploadsAndAcceptsTheNewBuild(t *testing.T) {
	edge := &fakeEdge{live: map[string]string{"blog": "d0"}, content: map[string][]byte{"d0": []byte("old")}}
	req, tunnel, done := request(t, edge, true)
	defer done()
	if err := Run(context.Background(), io.Discard, tunnel, req); err != nil {
		t.Fatal(err)
	}
	if edge.uploads != 1 || len(edge.rollbacks) != 0 || tunnel.opened != 1 || tunnel.stopped != 1 {
		t.Fatalf("uploads=%d rollbacks=%v tunnel=%d/%d", edge.uploads, edge.rollbacks, tunnel.opened, tunnel.stopped)
	}
}

func TestPublishRollsBackWhenThePublicAnswerIsWrong(t *testing.T) {
	edge := &fakeEdge{live: map[string]string{"blog": "d0"}, content: map[string][]byte{"d0": []byte("old")},
		serve: func([]byte) []byte { return []byte("a proxy error page") }}
	req, tunnel, done := request(t, edge, true)
	defer done()
	err := Run(context.Background(), io.Discard, tunnel, req)
	if err == nil || !strings.Contains(err.Error(), "back on d0") {
		t.Fatalf("err = %v", err)
	}
	if len(edge.rollbacks) != 1 || edge.rollbacks[0] != "d0" || edge.live["blog"] != "d0" {
		t.Fatalf("rollbacks = %v, live = %s", edge.rollbacks, edge.live["blog"])
	}
}

func TestPublishPlanTouchesNothingAndRefusesBadInput(t *testing.T) {
	edge := &fakeEdge{live: map[string]string{}, content: map[string][]byte{}}
	req, tunnel, done := request(t, edge, false)
	defer done()
	if err := Run(context.Background(), io.Discard, tunnel, req); err != nil || tunnel.opened != 0 {
		t.Fatalf("plan: %v, tunnel opened %d", err, tunnel.opened)
	}
	req.Apply = true
	req.Token = ""
	if err := Run(context.Background(), io.Discard, tunnel, req); err == nil || tunnel.opened != 0 {
		t.Fatal("published without a sites token")
	}
	req.Token, req.Site = "sites-token", "Bad Name"
	if err := Run(context.Background(), io.Discard, tunnel, req); err == nil {
		t.Fatal("accepted an invalid site name")
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(req.Dir, "leak")); err != nil {
		t.Fatal(err)
	}
	req.Site = "blog"
	if err := Run(context.Background(), io.Discard, tunnel, req); err == nil || edge.uploads != 0 {
		t.Fatal("packed a symlink out of the build")
	}
}
