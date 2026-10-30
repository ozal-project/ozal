//go:build !windows

package main

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ownGroup is a group the test process belongs to, so chown to it works
// without privileges.
func ownGroup(t *testing.T) *user.Group {
	t.Helper()
	g, err := user.LookupGroupId(strconv.Itoa(os.Getgid()))
	if err != nil {
		t.Skipf("primary group has no name: %v", err)
	}
	return g
}

func TestShareAdminSocketOpensItToTheGroupOnly(t *testing.T) {
	path := socketPath(t)
	if err := os.Chmod(filepath.Dir(path), 0o710); err != nil {
		t.Fatal(err)
	}
	listener, err := listenAdminSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := shareAdminSocket(path, ownGroup(t).Name); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o660 {
		t.Fatalf("shared socket mode = %o, want 660 (never world access)", perm)
	}
}

func TestShareAdminSocketWithoutGroupKeepsItPrivate(t *testing.T) {
	path := socketPath(t)
	listener, err := listenAdminSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := shareAdminSocket(path, ""); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

// A socket the group cannot reach must stop startup, not look shared.
func TestShareAdminSocketFailsClosed(t *testing.T) {
	path := socketPath(t)
	listener, err := listenAdminSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := shareAdminSocket(path, ownGroup(t).Name); err == nil || !strings.Contains(err.Error(), "RuntimeDirectoryMode") {
		t.Fatalf("closed directory accepted: %v", err)
	}
	if err := shareAdminSocket(path, "ozal-no-such-group-x"); err == nil {
		t.Fatal("unknown group accepted")
	}
	if os.Getuid() != 0 {
		if root, err := user.LookupGroupId("0"); err == nil {
			if err := shareAdminSocket(path, root.Name); err == nil || !strings.Contains(err.Error(), "SupplementaryGroups") {
				t.Fatalf("group the service is not in accepted: %v", err)
			}
		}
	}
}
