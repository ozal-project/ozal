package certs

import (
	"strings"
	"testing"
)

// FuzzCertificateNameRules keeps the pure part of ACME name handling aligned:
// every accepted name has a concrete probe, a safe storage key and the
// expected exact wildcard coverage boundary.
func FuzzCertificateNameRules(f *testing.F) {
	f.Add("example.com")
	f.Add("*.example.com")
	f.Add("*.sub.example.com")
	f.Add("*.com")
	f.Add(strings.Repeat("a", 64) + ".example.com")
	f.Add(strings.Repeat("a.", 126) + "a")

	f.Fuzz(func(t *testing.T, name string) {
		if err := ValidateName(name); err != nil {
			return
		}
		if got := ProbeName(name); !Covers(name, got) {
			t.Fatalf("accepted certificate name %q has uncovered probe %q", name, got)
		}
		if len(name) > 253 {
			t.Fatalf("accepted certificate name exceeds DNS length limit: %d bytes", len(name))
		}
		key := StorageKey(name)
		if strings.ContainsAny(key, "/*\\") {
			t.Fatalf("certificate name %q maps to unsafe storage key %q", name, key)
		}
		if ChallengeFQDN(name) != "_acme-challenge."+baseOf(name) {
			t.Fatalf("certificate name %q has an inconsistent ACME challenge name", name)
		}
		if IsWildcard(name) {
			base := baseOf(name)
			if !Covers(name, "probe."+base) || Covers(name, base) || Covers(name, "a.b."+base) {
				t.Fatalf("wildcard %q does not cover exactly one label", name)
			}
			if !strings.HasPrefix(key, wildcardDirPrefix) {
				t.Fatalf("wildcard %q has non-namespaced storage key %q", name, key)
			}
		} else if !Covers(name, strings.ToUpper(name)) || key != name {
			t.Fatalf("plain certificate name %q lost case-insensitive coverage or key identity", name)
		}
	})
}
