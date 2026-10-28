package certs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCloudflare is a minimal simulation of the Cloudflare DNS API. It
// records what a client asked for and answers with the shapes the adapter
// expects, so no test ever talks to the real API.
type fakeCloudflare struct {
	mu      sync.Mutex
	zones   map[string]string // zone name -> ID
	records map[string]map[string]string
	nextID  int

	created int
	deleted int

	status      int    // override the response status for the next call
	message     string // API error message for an unsuccessful envelope
	retryAfter  string
	failCreates bool
}

func newFakeCloudflare(t *testing.T, zones map[string]string) (*fakeCloudflare, *httptest.Server) {
	t.Helper()
	f := &fakeCloudflare{zones: zones, records: map[string]map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(server.Close)
	return f, server
}

func (f *fakeCloudflare) handle(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got == "" || !strings.HasPrefix(got, "Bearer ") {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	f.mu.Lock()
	status, message, retryAfter := f.status, f.message, f.retryAfter
	f.status, f.message, f.retryAfter = 0, "", ""
	f.mu.Unlock()
	if status != 0 {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1000,"message":"` + message + `"}]}`))
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/zones":
		name := r.URL.Query().Get("name")
		var result []map[string]string
		if id, ok := f.zones[name]; ok {
			result = append(result, map[string]string{"id": id, "name": name})
		}
		writeFakeCloudflare(w, http.StatusOK, result)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/zones/") && strings.HasSuffix(r.URL.Path, "/dns_records"):
		var body struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failCreates {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1000,"message":"internal"}]}`))
			return
		}
		f.nextID++
		id := "record-" + string(rune('0'+f.nextID))
		f.records[id] = map[string]string{"name": body.Name, "content": body.Content, "type": body.Type}
		f.created++
		writeFakeCloudflare(w, http.StatusOK, map[string]string{"id": id})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dns_records/batch"):
		var body struct {
			Deletes []struct{ ID string } `json:"deletes"`
			Posts   []map[string]any      `json:"posts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		for _, d := range body.Deletes {
			delete(f.records, d.ID)
			f.deleted++
		}
		for _, p := range body.Posts {
			f.nextID++
			f.records["record-"+string(rune('0'+f.nextID))] = map[string]string{"name": p["name"].(string), "content": p["content"].(string), "type": p["type"].(string)}
			f.created++
		}
		f.mu.Unlock()
		writeFakeCloudflare(w, http.StatusOK, map[string]any{})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/zones/") && strings.HasSuffix(r.URL.Path, "/dns_records"):
		name := r.URL.Query().Get("name")
		f.mu.Lock()
		result := []map[string]string{}
		for id, record := range f.records {
			if record["name"] == name {
				result = append(result, map[string]string{"id": id, "type": record["type"], "name": record["name"], "content": record["content"]})
			}
		}
		f.mu.Unlock()
		writeFakeCloudflare(w, http.StatusOK, result)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/zones/") && strings.Contains(r.URL.Path, "/dns_records/"):
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.records[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":81044,"message":"record not found"}]}`))
			return
		}
		delete(f.records, id)
		f.deleted++
		writeFakeCloudflare(w, http.StatusOK, map[string]string{"id": id})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func writeFakeCloudflare(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoded, _ := json.Marshal(map[string]any{"success": true, "errors": []any{}, "result": result})
	_, _ = w.Write(encoded)
}

func testCloudflareClient(t *testing.T, api *httptest.Server) *CloudflareClient {
	t.Helper()
	client, err := NewCloudflareClient(CloudflareConfig{Token: "test-token", BaseURL: api.URL, HTTPClient: api.Client()})
	if err != nil {
		t.Fatalf("NewCloudflareClient: %v", err)
	}
	return client
}

func TestCloudflareRequiresToken(t *testing.T) {
	if _, err := NewCloudflareClient(CloudflareConfig{}); err == nil {
		t.Fatal("an empty token must fail at composition time")
	}
}

func TestCloudflareCreateAndDeleteByID(t *testing.T) {
	fake, api := newFakeCloudflare(t, map[string]string{"example.com": "zone-1"})
	client := testCloudflareClient(t, api)
	ctx := context.Background()

	record, err := client.CreateTXT(ctx, "_acme-challenge.preview.example.com", "challenge-value")
	if err != nil {
		t.Fatalf("CreateTXT: %v", err)
	}
	if record.ID == "" || record.ZoneID != "zone-1" || record.Name != "_acme-challenge.preview.example.com" {
		t.Fatalf("record = %+v", record)
	}
	// The zone lookup walked preview.example.com and found example.com.
	if fake.created != 1 {
		t.Fatalf("created %d records, want 1", fake.created)
	}

	if err := client.DeleteTXT(ctx, record); err != nil {
		t.Fatalf("DeleteTXT: %v", err)
	}
	if fake.deleted != 1 || len(fake.records) != 0 {
		t.Fatalf("deleted %d, records left %d", fake.deleted, len(fake.records))
	}
	// Deleting an already-gone record is success, not an error.
	if err := client.DeleteTXT(ctx, record); err != nil {
		t.Fatalf("second DeleteTXT: %v", err)
	}
}

func TestCloudflareDeleteRefusesUnidentifiedRecord(t *testing.T) {
	_, api := newFakeCloudflare(t, map[string]string{"example.com": "zone-1"})
	client := testCloudflareClient(t, api)
	if err := client.DeleteTXT(context.Background(), DNSRecord{ZoneID: "zone-1"}); !errors.Is(err, ErrDNS) {
		t.Fatalf("err = %v, want ErrDNS", err)
	}
	if err := client.DeleteTXT(context.Background(), DNSRecord{ID: "record-1"}); !errors.Is(err, ErrDNS) {
		t.Fatalf("err = %v, want ErrDNS", err)
	}
}

func TestCloudflareClassifiesFailures(t *testing.T) {
	t.Run("api error is definite", func(t *testing.T) {
		fake, api := newFakeCloudflare(t, map[string]string{"example.com": "zone-1"})
		fake.status, fake.message = http.StatusBadRequest, "invalid token"
		client := testCloudflareClient(t, api)
		_, err := client.CreateTXT(context.Background(), "_acme-challenge.preview.example.com", "v")
		if err == nil || errors.Is(err, ErrAmbiguous) {
			t.Fatalf("err = %v, want a definite failure", err)
		}
	})
	t.Run("server error is ambiguous", func(t *testing.T) {
		fake, api := newFakeCloudflare(t, map[string]string{"example.com": "zone-1"})
		fake.status, fake.message = http.StatusBadGateway, "bad gateway"
		client := testCloudflareClient(t, api)
		_, err := client.CreateTXT(context.Background(), "_acme-challenge.preview.example.com", "v")
		if !errors.Is(err, ErrAmbiguous) {
			t.Fatalf("err = %v, want ErrAmbiguous", err)
		}
	})
	t.Run("transport error is ambiguous", func(t *testing.T) {
		fake, api := newFakeCloudflare(t, map[string]string{"example.com": "zone-1"})
		_ = fake
		api.Close()
		client := testCloudflareClient(t, api)
		_, err := client.CreateTXT(context.Background(), "_acme-challenge.preview.example.com", "v")
		if !errors.Is(err, ErrAmbiguous) {
			t.Fatalf("err = %v, want ErrAmbiguous", err)
		}
	})
	t.Run("rate limit keeps retry-after", func(t *testing.T) {
		fake, api := newFakeCloudflare(t, map[string]string{"example.com": "zone-1"})
		fake.status, fake.message, fake.retryAfter = http.StatusTooManyRequests, "slow down", "30"
		client := testCloudflareClient(t, api)
		_, err := client.CreateTXT(context.Background(), "_acme-challenge.preview.example.com", "v")
		var limited *RateLimitedError
		if !errors.As(err, &limited) || limited.RetryAfter != 30*time.Second {
			t.Fatalf("err = %v, want RateLimitedError with 30s", err)
		}
	})
	t.Run("no zone covers the name", func(t *testing.T) {
		_, api := newFakeCloudflare(t, map[string]string{"other.example": "zone-9"})
		client := testCloudflareClient(t, api)
		_, err := client.CreateTXT(context.Background(), "_acme-challenge.preview.example.com", "v")
		if !errors.Is(err, ErrDNS) {
			t.Fatalf("err = %v, want ErrDNS", err)
		}
	})
}
