// Package invitation holds the checks an invitation passes before it is
// issued, the same for the admin API and the offline break-glass command: a
// real email address, groups that exist, and what the person will be able to
// open. It is pure: callers bring the routes, the groups and the members.
package invitation

import (
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
)

// ValidEmail accepts a bare address (no display name) whose domain has at
// least two labels, such as ana@example.com. "ana@example" or "tu@correo"
// is refused: an invitation names an account by its email, and a typo would
// leave a person who can never match an email rule.
func ValidEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 || strings.TrimSpace(email) != email {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || addr.Name != "" {
		return false
	}
	at := strings.LastIndexByte(email, '@')
	local, domain := email[:at], email[at+1:]
	if local == "" || len(local) > 64 {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x80) {
				return false
			}
		}
	}
	// A top-level label of digits is an IP address, not a domain.
	return strings.Trim(labels[len(labels)-1], "0123456789") != ""
}

// KnownGroups are the groups this edge already has: created on purpose,
// held by someone, or required by an application.
func KnownGroups(defined []string, held [][]string, routes []config.Route) map[string]bool {
	known := map[string]bool{}
	for _, g := range defined {
		known[g] = true
	}
	for _, groups := range held {
		for _, g := range groups {
			known[g] = true
		}
	}
	for _, r := range routes {
		for _, g := range r.Policy().RequiredGroups {
			known[g] = true
		}
	}
	return known
}

// UnknownGroups lists the requested groups the edge does not have, in the
// order given. A new group needs an explicit confirmation: a typo would
// otherwise create one in silence and leave the person with nothing to open.
func UnknownGroups(requested []string, known map[string]bool) []string {
	var unknown []string
	for _, g := range requested {
		if !known[g] && !slices.Contains(unknown, g) {
			unknown = append(unknown, g)
		}
	}
	return unknown
}

// Suggest names the known group closest to a mistyped one, ignoring case,
// or "" when none is close.
func Suggest(group string, known map[string]bool) string {
	names := make([]string, 0, len(known))
	for k := range known {
		names = append(names, k)
	}
	slices.Sort(names)
	best, bestDistance := "", 3
	for _, k := range names {
		if d := distance(strings.ToLower(group), strings.ToLower(k)); d < bestDistance {
			best, bestDistance = k, d
		}
	}
	return best
}

// OpenableHosts are the protected applications whose identity policy would
// admit a person with this email and these groups, signed in with a passkey,
// the way the portal lists them. A rule that asks for a fresh login still
// counts: the door asks again.
func OpenableHosts(routes []config.Route, email string, groups []string, now time.Time) []string {
	principal := auth.Principal{Subject: "sb:invitation-preview", Email: email, EmailVerified: true, Groups: groups, AuthTime: now, Method: auth.MethodPasskey}
	var hosts []string
	for _, r := range routes {
		if !r.RequiresAuth() {
			continue
		}
		if auth.Explain(r, &principal).Decision.Allowed {
			hosts = append(hosts, r.Host)
		}
	}
	slices.Sort(hosts)
	return hosts
}

// distance is the Levenshtein edit distance between a and b.
func distance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur := make([]int, len(br)+1)
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(br)]
}
