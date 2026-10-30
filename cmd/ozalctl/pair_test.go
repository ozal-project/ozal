package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/ctl"
)

type pairRemoteStub struct{ terminal string }

func (r *pairRemoteStub) Run(context.Context, string, io.Reader) (string, error) { return "", nil }
func (r *pairRemoteStub) Terminal(_ context.Context, command string) (int, error) {
	r.terminal = command
	return 0, nil
}

func TestPairStatusToleratesLegacy404OnlyBeforeCandidateAPIIsRunning(t *testing.T) {
	for _, test := range []struct {
		name     string
		progress ctl.PairProgress
		want     string
	}{
		{name: "before cutover", progress: ctl.PairProgress{CheckedAt: "2026-10-30T00:00:00Z", Installed: "v0.1.0", Latest: "v0.2.0", Result: "updating", JobID: "job-1", Phase: "stopping"}, want: "202|404"},
		{name: "candidate running", progress: ctl.PairProgress{CheckedAt: "2026-10-30T00:00:00Z", Installed: "v0.1.0", Latest: "v0.2.0", Result: "updating", JobID: "job-1", Phase: "starting"}, want: "202"},
		{name: "already current", progress: ctl.PairProgress{CheckedAt: "2026-10-30T00:00:00Z", Installed: "v0.2.0", Latest: "v0.2.0", Result: "current", JobID: "job-1", Phase: "complete"}, want: "202|404"},
	} {
		t.Run(test.name, func(t *testing.T) {
			remote := &pairRemoteStub{}
			node := &pairSSHNode{remote: remote, socket: "/run/ozal/admin.sock"}
			if err := node.Report(context.Background(), test.progress); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(remote.terminal, "case \"$code\" in "+test.want+")") {
				t.Fatalf("report policy does not accept only %s: %s", test.want, remote.terminal)
			}
			if !strings.Contains(remote.terminal, "http://localhost/_/api/updates/status") {
				t.Fatalf("report did not use the fixed local status API: %s", remote.terminal)
			}
		})
	}
}

func (r *pairRemoteStub) Stream(_ context.Context, command string) (int, error) {
	r.terminal = command
	return 0, nil
}
