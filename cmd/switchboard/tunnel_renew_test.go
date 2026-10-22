package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRenewAgentSystemLayoutFlagsGoTogether(t *testing.T) {
	for _, args := range [][]string{
		{"-role", "edge", "-dir", "/var/lib/e"},
		{"-role", "edge", "-dir", "/var/lib/e", "-config", "/etc/e/config.json"},
		{"-role", "edge", "-dir", "relative", "-config", "/etc/e/config.json", "-unit", "u"},
	} {
		err := runTunnelRenewAgent(context.Background(), args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "go together") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
