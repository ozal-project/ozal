package certs

import (
	"strings"
	"testing"
)

func TestValidateNameEnforcesDNSNameLength(t *testing.T) {
	plain := strings.Join([]string{strings.Repeat("a", 63), strings.Repeat("b", 63), strings.Repeat("c", 63), strings.Repeat("d", 61)}, ".")
	if len(plain) != 253 {
		t.Fatalf("bad test fixture length: %d", len(plain))
	}
	if err := ValidateName(plain); err != nil {
		t.Fatalf("253-byte DNS name rejected: %v", err)
	}
	if err := ValidateName(plain + "a"); err == nil {
		t.Fatal("254-byte DNS name was accepted")
	}
	if err := ValidateName("*." + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)); err == nil {
		t.Fatal("wildcard name over 253 bytes was accepted")
	}
}

func TestCovers(t *testing.T) {
	tests := []struct {
		certName   string
		serverName string
		want       bool
	}{
		{"preview.example.com", "preview.example.com", true},
		{"preview.example.com", "PREVIEW.Example.COM", true},
		{"preview.example.com", "other.example.com", false},

		// A wildcard covers exactly one extra label (RFC 6125 section 6.4.3).
		{"*.example.com", "preview.example.com", true},
		{"*.example.com", "SIGEA.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "a.b.example.com", false},
		{"*.example.com", ".example.com", false},
		{"*.example.com", "evilexample.com", false},
		{"*.example.com", "preview.example.com.evil.net", false},

		{"*.example.com", "", false},
		{"preview.example.com", "", false},
	}
	for _, tt := range tests {
		if got := Covers(tt.certName, tt.serverName); got != tt.want {
			t.Errorf("Covers(%q, %q) = %v, want %v", tt.certName, tt.serverName, got, tt.want)
		}
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"preview.example.com", true},
		{"*.example.com", true},
		{"*.sub.example.com", true},

		{"", false},
		{"*", false},
		{"*.com", false}, // a wildcard over a public suffix is never what the operator means
		{"**.example.com", false},
		{"a.*.example.com", false},
		{"*example.com", false},
		{"*.*.example.com", false},
		{"example.com/path", false},
		{"exa mple.com", false},
		{"preview.example.com:443", false},
	}
	for _, tt := range tests {
		if err := ValidateName(tt.name); (err == nil) != tt.ok {
			t.Errorf("ValidateName(%q) = %v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}

func TestProbeName(t *testing.T) {
	if got := ProbeName("preview.example.com"); got != "preview.example.com" {
		t.Errorf("plain name changed: %q", got)
	}
	probe := ProbeName("*.example.com")
	if probe == "*.example.com" || !Covers("*.example.com", probe) {
		t.Errorf("ProbeName(*.example.com) = %q, want a concrete name the wildcard covers", probe)
	}
}

func TestChallengeFQDN(t *testing.T) {
	tests := map[string]string{
		"preview.example.com": "_acme-challenge.preview.example.com",
		"*.example.com":       "_acme-challenge.example.com",
		"*.sub.example.com":   "_acme-challenge.sub.example.com",
	}
	for name, want := range tests {
		if got := ChallengeFQDN(name); got != want {
			t.Errorf("ChallengeFQDN(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestStorageKey(t *testing.T) {
	if got := StorageKey("*.example.com"); got != "_wildcard.example.com" {
		t.Errorf("StorageKey(*.example.com) = %q", got)
	}
	if got := StorageKey("preview.example.com"); got != "preview.example.com" {
		t.Errorf("StorageKey(plain) = %q", got)
	}
}
