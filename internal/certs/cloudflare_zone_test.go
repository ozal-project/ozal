package certs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// EDGE-009: zone setup uses exactly these endpoints and verbs, never a
// delete, and the uploaded key never comes back in an error.
func TestZoneSetupEndpoints(t *testing.T) {
	var seen []string
	var uploaded map[string]string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing token on %s", r.URL.Path)
		}
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/zones":
			writeFakeCloudflare(w, 200, []map[string]string{{"id": "z1", "name": "example.dev"}})
		case r.URL.Path == "/zones/z1/settings/ssl" && r.Method == http.MethodGet:
			writeFakeCloudflare(w, 200, map[string]string{"id": "ssl", "value": "flexible"})
		case r.URL.Path == "/zones/z1/settings/ssl" && r.Method == http.MethodPatch:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["value"] != "strict" {
				t.Errorf("ssl patch = %v", body)
			}
			writeFakeCloudflare(w, 200, body)
		case r.URL.Path == "/zones/z1/origin_tls_client_auth/settings":
			writeFakeCloudflare(w, 200, map[string]bool{"enabled": r.Method == http.MethodPut})
		case r.URL.Path == "/zones/z1/origin_tls_client_auth" && r.Method == http.MethodGet:
			writeFakeCloudflare(w, 200, []map[string]string{{"id": "old", "certificate": "PEM", "status": "active"}})
		case r.URL.Path == "/zones/z1/origin_tls_client_auth" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&uploaded)
			writeFakeCloudflare(w, 200, map[string]string{"id": "new", "status": "pending_deployment"})
		case r.URL.Path == "/zones/z1/dns_records" && r.Method == http.MethodGet:
			writeFakeCloudflare(w, 200, []map[string]string{})
		case r.URL.Path == "/zones/z1/dns_records" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["type"] != "CNAME" || body["proxied"] != false {
				t.Errorf("record = %v", body)
			}
			writeFakeCloudflare(w, 200, map[string]string{"id": "r1"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer api.Close()
	c := testCloudflareClient(t, api)
	ctx := context.Background()

	if mode, err := c.ZoneSSLMode(ctx, "example.dev"); err != nil || mode != "flexible" {
		t.Fatalf("mode %q, %v", mode, err)
	}
	if err := c.SetZoneSSLStrict(ctx, "example.dev"); err != nil {
		t.Fatal(err)
	}
	if on, list, err := c.ZoneOriginPull(ctx, "example.dev"); err != nil || on || len(list) != 1 || list[0].ID != "old" {
		t.Fatalf("pull %v %+v %v", on, list, err)
	}
	if created, err := c.UploadZoneClientCertificate(ctx, "example.dev", []byte("CERT"), []byte("KEY")); err != nil || created.ID != "new" {
		t.Fatalf("upload %+v %v", created, err)
	}
	if uploaded["certificate"] != "CERT" || uploaded["private_key"] != "KEY" {
		t.Fatalf("upload body = %v", uploaded)
	}
	if err := c.EnableZoneOriginPull(ctx, "example.dev"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateChallengeCNAME(ctx, "_acme-challenge.example.dev", "example-dev.acme.edge.net"); err != nil {
		t.Fatal(err)
	}
	for _, call := range seen {
		if strings.HasPrefix(call, http.MethodDelete) {
			t.Fatalf("zone setup deleted something: %s", call)
		}
	}
}

func TestZoneUploadErrorDoesNotRepeatTheKey(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zones" {
			writeFakeCloudflare(w, 200, []map[string]string{{"id": "z1", "name": "example.dev"}})
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
	}))
	defer api.Close()
	_, err := testCloudflareClient(t, api).UploadZoneClientCertificate(context.Background(), "example.dev", []byte("CERT"), []byte("SECRET-KEY-MATERIAL"))
	if err == nil || strings.Contains(err.Error(), "SECRET-KEY-MATERIAL") || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("err = %v", err)
	}
	// A refused name is not ambiguous: nothing was created.
	if errors.Is(err, ErrAmbiguous) {
		t.Fatalf("a 403 was reported as ambiguous: %v", err)
	}
}
