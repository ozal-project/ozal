package auth

import (
	"net/url"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

// FuzzSafeReturnTo checks the open-redirect guard: anything returned must be
// an absolute HTTP(S) URL without userinfo on an allowed host, and feeding it
// back must be a fixed point.
func FuzzSafeReturnTo(f *testing.F) {
	gate := &Gate{allowedHosts: map[string]bool{"app.example.com": true}}
	f.Add("https://app.example.com/dashboard?x=1")
	f.Add("https://evil.example.com/")
	f.Add("//app.example.com/path")
	f.Add("javascript://app.example.com/%0aalert(1)")
	f.Add("")

	f.Fuzz(func(t *testing.T, raw string) {
		got := gate.safeReturnTo(raw)
		if got == "/" {
			return
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("safeReturnTo(%q) = %q, unparsable: %v", raw, got, err)
		}
		if u.User != nil {
			t.Fatalf("safeReturnTo(%q) returned userinfo: %q", raw, got)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Fatalf("safeReturnTo(%q) returned scheme %q", raw, u.Scheme)
		}
		host, err := config.NormalizeHost(u.Host)
		if err != nil || !gate.allowedHosts[host] {
			t.Fatalf("safeReturnTo(%q) returned disallowed host %q", raw, u.Host)
		}
		if again := gate.safeReturnTo(got); again != got {
			t.Fatalf("safeReturnTo is not idempotent: %q -> %q", got, again)
		}
	})
}
