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

// fakeHetzner simulates the RRSet action endpoints: records are identified
// by value inside a set, every change returns an Action that completes after
// `slow` polls, and the behaviours the adapter must classify can be forced.
type fakeHetzner struct {
	t     *testing.T
	srv   *httptest.Server
	token string
	zone  string

	mu       sync.Mutex
	sets     map[string]map[string]bool // "name/type" -> value set
	actions  map[int64]int              // id -> polls left
	failures map[int64]bool             // actions that end in error
	nextID   int64
	slow     int
	force    int // status to answer on POST, 0 = normal
	stuck    bool
	failNext bool
	requests []string
	bodies   []map[string]any
}

func newFakeHetzner(t *testing.T) *fakeHetzner {
	f := &fakeHetzner{t: t, token: "secret-token", zone: "example.test", sets: map[string]map[string]bool{},
		actions: map[int64]int{}, failures: map[int64]bool{}, nextID: 41}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHetzner) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		writeHetznerJSON(w, 401, map[string]any{"error": map[string]any{"code": "unauthorized", "message": "unable to authenticate"}})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "zones" && parts[1] == "actions" {
		var id int64
		for _, c := range parts[2] {
			id = id*10 + int64(c-'0')
		}
		left, ok := f.actions[id]
		if !ok {
			writeHetznerJSON(w, 404, map[string]any{"error": map[string]any{"code": "not_found", "message": "action not found"}})
			return
		}
		status := "running"
		if f.stuck {
			status = "running"
		} else if left <= 0 {
			status = "success"
			if f.failures[id] {
				status = "error"
			}
		} else {
			f.actions[id] = left - 1
		}
		writeHetznerJSON(w, 200, map[string]any{"action": map[string]any{"id": id, "status": status}})
		return
	}
	// POST /zones/{zone}/rrsets/{name}/TXT/actions/{add|remove}_records
	if r.Method != http.MethodPost || len(parts) != 7 || parts[0] != "zones" || parts[2] != "rrsets" || parts[4] != "TXT" || parts[5] != "actions" {
		writeHetznerJSON(w, 400, map[string]any{"error": map[string]any{"code": "invalid_input", "message": "bad path"}})
		return
	}
	if parts[1] != f.zone {
		writeHetznerJSON(w, 404, map[string]any{"error": map[string]any{"code": "not_found", "message": "zone not found"}})
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.bodies = append(f.bodies, body)
	if f.force != 0 {
		writeHetznerJSON(w, f.force, map[string]any{"error": map[string]any{"code": "forced", "message": "forced"}})
		return
	}
	key := parts[3] + "/TXT"
	records, _ := body["records"].([]any)
	switch parts[6] {
	case "add_records":
		if f.sets[key] == nil {
			f.sets[key] = map[string]bool{}
		}
		for _, rec := range records {
			f.sets[key][rec.(map[string]any)["value"].(string)] = true
		}
	case "remove_records":
		if f.sets[key] == nil {
			writeHetznerJSON(w, 404, map[string]any{"error": map[string]any{"code": "not_found", "message": "rrset not found"}})
			return
		}
		for _, rec := range records {
			delete(f.sets[key], rec.(map[string]any)["value"].(string))
		}
		if len(f.sets[key]) == 0 {
			delete(f.sets, key)
		}
	}
	f.nextID++
	f.actions[f.nextID] = f.slow
	if f.failNext {
		f.failures[f.nextID] = true
		f.failNext = false
	}
	writeHetznerJSON(w, 201, map[string]any{"action": map[string]any{"id": f.nextID, "status": "running"}})
}

func writeHetznerJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeHetzner) has(name, value string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sets[name+"/TXT"][`"`+value+`"`]
}

func (f *fakeHetzner) client(t *testing.T, mutate func(*HetznerConfig)) *HetznerClient {
	cfg := HetznerConfig{Token: f.token, Zone: "Example.Test.", BaseURL: f.srv.URL, PollInterval: time.Millisecond, ActionTimeout: 500 * time.Millisecond}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := NewHetznerClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHetznerCreateAndDeleteExactRecord(t *testing.T) {
	f := newFakeHetzner(t)
	f.slow = 2 // the change is only applied once the Action finishes
	c := f.client(t, nil)
	ctx := context.Background()

	if _, err := c.CreateTXT(ctx, "_acme-challenge.example.test", "other-value"); err != nil {
		t.Fatal(err)
	}
	rec, err := c.CreateTXT(ctx, "_ACME-challenge.Example.test.", challenge)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ZoneID != "example.test" || rec.Name != "_acme-challenge.example.test" || rec.Value != challenge || rec.ID == "" {
		t.Fatalf("record = %+v", rec)
	}
	if !f.has("_acme-challenge", challenge) || !f.has("_acme-challenge", "other-value") {
		t.Fatal("create did not reach the API")
	}
	if got := f.bodies[0]["ttl"]; got != float64(60) {
		t.Fatalf("ttl = %v, want the 60 s minimum", got)
	}

	if err := c.DeleteTXT(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if f.has("_acme-challenge", challenge) || !f.has("_acme-challenge", "other-value") {
		t.Fatal("delete must remove only the record it created")
	}
	if err := c.DeleteTXT(ctx, rec); err != nil {
		t.Fatal("deleting a record whose set is already gone must succeed:", err)
	}
}

func TestHetznerWaitsForTheActionBeforeReportingSuccess(t *testing.T) {
	f := newFakeHetzner(t)
	f.slow = 3
	c := f.client(t, nil)
	if _, err := c.CreateTXT(context.Background(), "_acme-challenge.example.test", challenge); err != nil {
		t.Fatal(err)
	}
	polls := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, "GET /zones/actions/") {
			polls++
		}
	}
	if polls < 3 {
		t.Fatalf("polled the action %d times, want at least 3", polls)
	}
}

func TestHetznerApexAndNamesOutsideTheZone(t *testing.T) {
	f := newFakeHetzner(t)
	c := f.client(t, nil)
	if _, err := c.CreateTXT(context.Background(), "example.test", challenge); err != nil {
		t.Fatal(err)
	}
	if f.requests[0] != "POST /zones/example.test/rrsets/@/TXT/actions/add_records" {
		t.Fatalf("apex request = %q", f.requests[0])
	}
	calls := len(f.requests)
	for _, name := range []string{"_acme-challenge.other.test", "example.test.evil.test", "notexample.test"} {
		if _, err := c.CreateTXT(context.Background(), name, challenge); !errors.Is(err, ErrDNS) || errors.Is(err, ErrAmbiguous) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(f.requests) != calls {
		t.Fatal("a name outside the zone reached the API")
	}
}

func TestHetznerRefusesToDeleteWhatItDidNotCreate(t *testing.T) {
	f := newFakeHetzner(t)
	c := f.client(t, nil)
	good, err := c.CreateTXT(context.Background(), "_acme-challenge.example.test", challenge)
	if err != nil {
		t.Fatal(err)
	}
	calls := len(f.requests)
	for name, rec := range map[string]DNSRecord{
		"no id":       {ZoneID: good.ZoneID, Name: good.Name, Value: good.Value},
		"no value":    {ZoneID: good.ZoneID, ID: good.ID, Name: good.Name},
		"other zone":  {ZoneID: "other.test", ID: good.ID, Name: good.Name, Value: good.Value},
		"forged id":   {ZoneID: good.ZoneID, ID: "hetzner:0000000000000000", Name: good.Name, Value: good.Value},
		"other value": {ZoneID: good.ZoneID, ID: good.ID, Name: good.Name, Value: "other"},
		"other name":  {ZoneID: good.ZoneID, ID: good.ID, Name: "www.example.test", Value: good.Value},
	} {
		if err := c.DeleteTXT(context.Background(), rec); !errors.Is(err, ErrDNS) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if len(f.requests) != calls || !f.has("_acme-challenge", challenge) {
		t.Fatal("a refused delete reached the API")
	}
}

func TestHetznerOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(*fakeHetzner)
		mutate    func(*HetznerConfig)
		ambiguous bool
	}{
		{"server error may have applied", func(f *fakeHetzner) { f.force = 503 }, nil, true},
		{"rate limited", func(f *fakeHetzner) { f.force = 429 }, nil, false},
		{"validation error is clean", func(f *fakeHetzner) { f.force = 422 }, nil, false},
		{"wrong token is clean", nil, func(c *HetznerConfig) { c.Token = "wrong" }, false},
		{"action ends in error is clean", func(f *fakeHetzner) { f.failNext = true; f.slow = 1 }, nil, false},
		{"action never finishes", func(f *fakeHetzner) { f.stuck = true }, func(c *HetznerConfig) { c.ActionTimeout = 30 * time.Millisecond }, true},
		{"zone not found is clean", nil, func(c *HetznerConfig) { c.Zone = "missing.test" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHetzner(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			c := f.client(t, tc.mutate)
			name := "_acme-challenge.example.test"
			if tc.name == "zone not found is clean" {
				name = "_acme-challenge.missing.test"
			}
			_, err := c.CreateTXT(context.Background(), name, challenge)
			if err == nil {
				t.Fatal("expected an error")
			}
			var limited *RateLimitedError
			if tc.name == "rate limited" {
				if !errors.As(err, &limited) {
					t.Fatalf("want RateLimitedError, got %v", err)
				}
				return
			}
			if got := errors.Is(err, ErrAmbiguous); got != tc.ambiguous {
				t.Fatalf("ambiguous = %v, want %v (%v)", got, tc.ambiguous, err)
			}
			if !tc.ambiguous && !errors.Is(err, ErrDNS) {
				t.Fatalf("clean failures wrap ErrDNS: %v", err)
			}
			if strings.Contains(err.Error(), f.token) {
				t.Fatalf("the error leaks the token: %v", err)
			}
		})
	}
}

func TestHetznerUnreachableAPIIsAmbiguous(t *testing.T) {
	f := newFakeHetzner(t)
	c := f.client(t, func(cfg *HetznerConfig) { cfg.BaseURL = "http://127.0.0.1:1" })
	if _, err := c.CreateTXT(context.Background(), "_acme-challenge.example.test", challenge); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("a lost response may hide an applied change: %v", err)
	}
}

func TestHetznerTXTQuotingIsOneDecision(t *testing.T) {
	got, err := hetznerTXTValue(challenge)
	if err != nil || got != `"`+challenge+`"` {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", strings.Repeat("x", 256), `a"b`, `a\b`, "a\nb", "a\x7fb"} {
		if _, err := hetznerTXTValue(bad); !errors.Is(err, ErrDNS) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestHetznerConfigValidation(t *testing.T) {
	for name, cfg := range map[string]HetznerConfig{
		"no token":     {Zone: "example.test"},
		"blank token":  {Token: "  ", Zone: "example.test"},
		"no zone":      {Token: "t"},
		"dot zone":     {Token: "t", Zone: "."},
		"path in zone": {Token: "t", Zone: "example.test/../x"},
		"long label":   {Token: "t", Zone: strings.Repeat("a", 64) + ".test"},
	} {
		if _, err := NewHetznerClient(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
