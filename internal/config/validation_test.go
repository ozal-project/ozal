package config

import "testing"

func TestNormalizeHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "EXAMPLE.com.", want: "example.com"},
		{input: "Magiclink.Localhost.:8888", want: "magiclink.localhost:8888"},
		{input: "[2001:0db8::1]:443", want: "[2001:db8::1]:443"},
		{input: "2001:0db8::1", want: "[2001:db8::1]"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := NormalizeHost(tt.input)
			if err != nil {
				t.Fatalf("NormalizeHost(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeHost(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeHostRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{
		"",
		" example.com",
		"example.com ",
		"example.com:0",
		"example.com:",
		"example.com:65536",
		"example.com:not-a-port",
		"evil/example.com",
		".example.com",
		"example..com",
		"[example.com]",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := NormalizeHost(input); err == nil {
				t.Fatalf("NormalizeHost(%q) returned nil error", input)
			}
		})
	}
}

func TestParseTarget(t *testing.T) {
	for _, target := range []string{
		"http://localhost:9000",
		"https://upstream.example.com/base",
		"HTTPS://upstream.example.com",
	} {
		t.Run("accepts "+target, func(t *testing.T) {
			if _, err := ParseTarget(target); err != nil {
				t.Fatalf("ParseTarget(%q): %v", target, err)
			}
		})
	}

	for _, target := range []string{
		"ftp://upstream.example.com",
		"localhost:9000",
		"/relative",
		"http:///missing-host",
	} {
		t.Run("rejects "+target, func(t *testing.T) {
			if _, err := ParseTarget(target); err == nil {
				t.Fatalf("ParseTarget(%q) returned nil error", target)
			}
		})
	}
}

func TestCookieDomainCoversOnlyDomainBoundary(t *testing.T) {
	if !cookieDomainCovers("app.example.com:8443", "example.com") {
		t.Fatal("expected example.com to cover app.example.com")
	}
	if !cookieDomainCovers("example.com", "example.com") {
		t.Fatal("expected example.com to cover itself")
	}
	if cookieDomainCovers("evilexample.com", "example.com") {
		t.Fatal("expected example.com not to cover evilexample.com")
	}
}
