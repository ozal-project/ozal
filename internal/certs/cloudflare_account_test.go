package certs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestVerifyTokenAndListZones(t *testing.T) {
	var auth []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/user/tokens/verify":
			w.Write([]byte(`{"success":true,"result":{"status":"active"}}`))
		case r.URL.Path == "/zones" && r.URL.Query().Get("page") == "1":
			w.Write([]byte(`{"success":true,"result":[{"name":"Example.com."},{"name":"example.org"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewCloudflareClient(CloudflareConfig{Token: "tok", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.VerifyToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	zones, err := client.ZoneNames(context.Background())
	if err != nil || !slices.Equal(zones, []string{"example.com", "example.org"}) {
		t.Fatalf("zones = %v, %v", zones, err)
	}
	for _, a := range auth {
		if a != "Bearer tok" {
			t.Fatalf("authorization = %q", a)
		}
	}
}

func TestVerifyTokenRefusesAnInactiveTokenAndNoZones(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/user/tokens/verify" {
			w.Write([]byte(`{"success":true,"result":{"status":"disabled"}}`))
			return
		}
		w.Write([]byte(`{"success":true,"result":[]}`))
	}))
	defer server.Close()
	client, _ := NewCloudflareClient(CloudflareConfig{Token: "tok", BaseURL: server.URL})
	if err := client.VerifyToken(context.Background()); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("inactive token: %v", err)
	}
	if _, err := client.ZoneNames(context.Background()); err == nil || !strings.Contains(err.Error(), "Zone · Read") {
		t.Fatalf("no zones: %v", err)
	}
}

func TestCloudflareRefusalCannotEchoTheToken(t *testing.T) {
	const token = "dummy-cloudflare-token-for-this-test"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"success":false,"errors":[{"code":1000,"message":"refused ` + token + `"}]}`))
	}))
	defer server.Close()
	client, err := NewCloudflareClient(CloudflareConfig{Token: token, BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.VerifyToken(context.Background()); err == nil || strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatal("an upstream error did not redact the token")
	}
}
