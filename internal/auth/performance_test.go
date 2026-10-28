package auth

import (
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/config"
)

func BenchmarkAuthorizeEmailAndGroup(b *testing.B) {
	principal := Principal{
		Email:         "Alice@Example.com",
		EmailVerified: true,
		Groups:        []string{"ops"},
	}
	policy := config.AccessPolicy{
		AllowedEmails:  []string{"alice@example.com"},
		RequiredGroups: []string{"ops"},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if decision := Authorize(principal, policy); !decision.Allowed {
			b.Fatal("benchmark principal unexpectedly denied")
		}
	}
}

func BenchmarkFreshPasskey(b *testing.B) {
	principal := Principal{Method: MethodPasskey}
	policy := config.AccessPolicy{Methods: []string{MethodPasskey}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if decision := Fresh(principal, policy, time.Time{}); !decision.Allowed {
			b.Fatal("benchmark passkey unexpectedly required step-up")
		}
	}
}
