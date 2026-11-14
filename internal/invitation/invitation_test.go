package invitation

import (
	"slices"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

func TestValidEmailNeedsARealDomain(t *testing.T) {
	for _, ok := range []string{"ana@example.com", "a.b+tag@mail.example.co", "ops@xn--bcher-kva.example", "x@a-b.io"} {
		if !ValidEmail(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"tu@correo", "ana@example", "ana", "@example.com", "ana@", "Ana <ana@example.com>", " ana@example.com", "ana@.example.com", "ana@example..com", "ana@-example.com", "ana@example.com.", "ana@10.0.0.1", "ana@exa_mple.com", ""} {
		if ValidEmail(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestUnknownGroupsAndSuggestion(t *testing.T) {
	routes := []config.Route{{Host: "wiki.example.com", Access: &config.AccessPolicy{RequiredGroups: []string{"engineering"}}}}
	known := KnownGroups([]string{"ops"}, [][]string{{"finance"}}, routes)
	for _, g := range []string{"ops", "finance", "engineering"} {
		if !known[g] {
			t.Errorf("%s not known", g)
		}
	}
	if got := UnknownGroups([]string{"ops", "GRUPO", "enginering", "GRUPO"}, known); !slices.Equal(got, []string{"GRUPO", "enginering"}) {
		t.Fatalf("unknown = %v", got)
	}
	if got := Suggest("enginering", known); got != "engineering" {
		t.Fatalf("suggest = %q", got)
	}
	if got := Suggest("GRUPO", known); got != "" {
		t.Fatalf("suggested %q for a name nothing like a group", got)
	}
}

// What an invitation will open follows the same policy the portal uses.
func TestOpenableHostsFollowThePolicy(t *testing.T) {
	routes := []config.Route{
		{Host: "wiki.example.com", Access: &config.AccessPolicy{RequiredGroups: []string{"engineering"}}},
		{Host: "pay.example.com", Access: &config.AccessPolicy{RequiredGroups: []string{"finance"}}},
		{Host: "public.example.com"},
	}
	now := time.Now()
	if got := OpenableHosts(routes, "ana@example.com", []string{"engineering"}, now); !slices.Equal(got, []string{"wiki.example.com"}) {
		t.Fatalf("engineering opens %v", got)
	}
	if got := OpenableHosts(routes, "ana@example.com", []string{"nobody"}, now); len(got) != 0 {
		t.Fatalf("an unused group opens %v", got)
	}
}
