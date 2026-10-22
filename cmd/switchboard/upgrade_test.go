package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestUpgradeRefusesIncompletePlansBeforeTouchingAnything(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-unit", "sb"}, "usage"},
		{[]string{"-unit", "sb", "-binary", "relative/switchboard", "-sha256", "ab", "-config", "c.json"}, "absolute"},
		{[]string{"-unit", "sb", "-binary", "/usr/local/bin/switchboard", "-sha256", "ab", "-config", "c.json", "-db", "/var/lib/sb/state.db"}, "-db needs -user"},
		{[]string{"-unit", "sb", "-binary", "/usr/local/bin/switchboard", "-sha256", "ab", "-config", "c.json", "-keep", "0"}, "-keep"},
	} {
		err := runUpgrade(context.Background(), c.args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}
