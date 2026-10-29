//go:build linux

package install

import (
	"os/user"
	"testing"
)

func TestServiceAccountMatchesTheUnitAndTokenGroup(t *testing.T) {
	for _, tc := range []struct {
		name, uid, gid, unitGID string
		valid                   bool
	}{
		{"dedicated account", "991", "992", "992", true},
		{"existing mismatched primary group", "991", "993", "992", false},
		{"root user", "0", "992", "992", false},
		{"root group", "991", "0", "0", false},
		{"malformed identity", "unknown", "992", "992", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account, err := serviceAccount(&user.User{Uid: tc.uid, Gid: tc.gid}, &user.Group{Gid: tc.unitGID})
			if (err == nil) != tc.valid {
				t.Fatal(account, err)
			}
		})
	}
}
