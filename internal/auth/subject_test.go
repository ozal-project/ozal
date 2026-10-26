package auth

import "testing"

func TestOIDCSubjectsHaveDistinctAuthority(t *testing.T) {
	subjects := []string{"sb:123", "123", "a|b", "oidc:a.b", "é/😺"}
	seen := map[string]bool{}
	for _, issuer := range []string{"https://one.example", "https://two.example"} {
		for _, sub := range subjects {
			value := OIDCSubject(issuer, sub)
			if seen[value] || !IsOIDCSubject(issuer, value) || IsOIDCSubject(issuer+"/other", value) {
				t.Fatalf("ambiguous subject %s", value)
			}
			seen[value] = true
		}
	}
	for _, legacy := range []string{"sb:123", "123", "oidc:a.b", OIDCSubject("https://one.example", "")} {
		if IsOIDCSubject("https://one.example", legacy) {
			t.Fatal("accepted legacy or empty subject")
		}
	}
}
