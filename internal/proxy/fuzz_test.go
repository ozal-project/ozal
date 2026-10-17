package proxy

import (
	"strings"
	"testing"
)

// FuzzStripCookie checks that stripping a named cookie never leaves it behind
// and is idempotent, for arbitrary header values.
func FuzzStripCookie(f *testing.F) {
	f.Add("sb_session=secret; theme=dark", "sb_session")
	f.Add("a=1; sb_session=x; b=2", "sb_session")
	f.Add("sb_session", "sb_session")
	f.Add("", "sb_session")
	f.Add("token=a=b; sb_session=x", "sb_session")

	f.Fuzz(func(t *testing.T, header, name string) {
		if name == "" {
			return
		}
		once := stripCookie(header, name)
		if twice := stripCookie(once, name); twice != once {
			t.Fatalf("stripCookie is not idempotent: %q -> %q -> %q", header, once, twice)
		}
		for _, pair := range strings.Split(once, ";") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			gotName, _, _ := strings.Cut(pair, "=")
			if strings.TrimSpace(gotName) == name {
				t.Fatalf("stripCookie(%q, %q) kept %q", header, name, pair)
			}
		}
	})
}
