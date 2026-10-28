package certs

import (
	"context"
	"errors"
	"testing"
)

func TestCreateSiteRecordNeverReplacesAnAddressRecord(t *testing.T) {
	fake, api := newFakeCloudflare(t, map[string]string{"example.com": "zone-1"})
	client := testCloudflareClient(t, api)
	ctx := context.Background()

	// A TXT record at the same name is not an address record: it stays and
	// does not block the A record.
	if _, err := client.CreateTXT(ctx, "testing.example.com", "verification"); err != nil {
		t.Fatalf("CreateTXT: %v", err)
	}
	created, err := client.CreateSiteRecord(ctx, SiteRecord{Type: "A", Name: "testing.example.com", Content: "203.0.113.10", Proxied: true})
	if err != nil || created.ID == "" {
		t.Fatalf("CreateSiteRecord = %+v, %v", created, err)
	}
	// The name now has an address record (perhaps the live hosting): a
	// second create is refused and nothing is written.
	before := fake.created
	if _, err := client.CreateSiteRecord(ctx, SiteRecord{Type: "CNAME", Name: "testing.example.com", Content: "edge.example.com"}); !errors.Is(err, ErrRecordExists) {
		t.Fatalf("second create err = %v, want ErrRecordExists", err)
	}
	if fake.created != before {
		t.Fatal("a refused create still wrote a record")
	}
	if _, err := client.CreateSiteRecord(ctx, SiteRecord{Type: "TXT", Name: "other.example.com", Content: "x"}); err == nil {
		t.Fatal("a non-address record type was accepted")
	}
}

func TestAutomaticNameFollowsRoutesAndRefusesUnissuableNames(t *testing.T) {
	routes := []string{"app.example.com", "alt.example.org:443", "dev.example.net:8443", "local.localhost:8888"}
	for name, want := range map[string]bool{
		"app.example.com":    true,  // a configured route
		"APP.example.com.":   true,  // case and trailing dot
		"alt.example.org":    true,  // a route on the HTTPS port
		"other.example.com":  false, // not served here: the regression
		"dev.example.net":    false, // a route on another port is not on :443
		"local.localhost":    false, // a CA refuses .localhost
		"192.0.2.1":          false,
		"localhost":          false,
		"*.example.com":      false,
		"covered.example.io": true, // under a configured wildcard
	} {
		if got := AutomaticName(name, []string{"*.example.io"}, routes); got != want {
			t.Errorf("AutomaticName(%q) = %v, want %v", name, got, want)
		}
	}
	if AutomaticName("app.example.com", nil, nil) {
		t.Error("with no routes and no configured names nothing is eligible")
	}
}
