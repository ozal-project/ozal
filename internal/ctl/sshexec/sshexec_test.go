package sshexec

import "testing"

func TestRemoteSeparatesTheDestinationFromOptions(t *testing.T) {
	remote := Remote{Target: "-oProxyCommand=malicious", Key: "/tmp/key"}
	args := remote.base("-o", "BatchMode=yes")
	if len(args) < 2 {
		t.Fatalf("base() = %v", args)
	}
	if got, want := args[len(args)-2], "--"; got != want {
		t.Fatalf("argument before the target = %q, want %q in %v", got, want, args)
	}
	if got, want := args[len(args)-1], remote.Target; got != want {
		t.Fatalf("last argument = %q, want the target %q", got, want)
	}
}
