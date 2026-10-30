package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/ctl"
)

type pairRemoteStub struct {
	terminal  string
	commands  []string
	runResult func(string) (string, error)
	termCode  int
}

func (r *pairRemoteStub) Run(_ context.Context, command string, _ io.Reader) (string, error) {
	r.commands = append(r.commands, command)
	if r.runResult != nil {
		return r.runResult(command)
	}
	return "free", nil
}
func (r *pairRemoteStub) Terminal(_ context.Context, command string) (int, error) {
	r.terminal = command
	return r.termCode, nil
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

func TestPairInspectCapturesEffectiveSystemdGroupAndRejectsTargetMismatch(t *testing.T) {
	remote := &pairRemoteStub{runResult: func(command string) (string, error) {
		switch {
		case strings.HasPrefix(command, "sha256sum "):
			return strings.Repeat("a", 64) + " /usr/local/lib/ozal/ozal\n", nil
		case strings.Contains(command, " version -json"):
			return `{"version":"0.1.0","commit":"abc123","os":"linux","arch":"amd64"}`, nil
		case strings.Contains(command, "systemctl show"):
			return "User=ozal\nGroup=ozal-state\n", nil
		default:
			return "", nil
		}
	}}
	node := &pairSSHNode{remote: remote, name: "edge-edge", role: "edge", unit: "ozal.service", user: "ozal", binary: "/usr/local/lib/ozal/ozal", socket: "/run/ozal/admin.sock"}
	state, err := node.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.ServiceUser != "ozal" || state.ServiceGroup != "ozal-state" {
		t.Fatalf("service identity = %s:%s", state.ServiceUser, state.ServiceGroup)
	}
	node.serviceGroup = "ozal"
	if _, err := node.Inspect(context.Background()); err == nil || !strings.Contains(err.Error(), "target declares ozal") {
		t.Fatalf("mismatched configured group accepted: %v", err)
	}
}

func TestPairRestoreUsesCapturedSystemdGroup(t *testing.T) {
	remote := &pairRemoteStub{}
	node := &pairSSHNode{remote: remote, role: "edge", user: "ozal", binary: "/usr/local/lib/ozal/ozal", db: "/var/lib/ozal/state.db", backup: "/usr/local/lib/ozal/ozal.pair-job.bak", dbBackup: "/var/lib/ozal/state.db.pair-job.bak.db", job: "job"}
	state := ctl.PairState{ServiceUser: "ozal", ServiceGroup: "ozal-state"}
	if err := node.Restore(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.terminal, "chown") || !strings.Contains(remote.terminal, "ozal:ozal-state") {
		t.Fatalf("rollback did not preserve effective group: %s", remote.terminal)
	}
	if err := node.Restore(context.Background(), ctl.PairState{ServiceUser: "ozal"}); err == nil {
		t.Fatal("SQLite restore proceeded without a captured group")
	}
}

func TestPairPreflightRunsReadOnlyHostChecks(t *testing.T) {
	remote := &pairRemoteStub{runResult: func(command string) (string, error) {
		if strings.Contains(command, "printf held") {
			return "free", nil
		}
		if strings.HasPrefix(command, "sha256sum ") {
			return strings.Repeat("a", 64) + " /usr/local/lib/ozal/ozal\n", nil
		}
		if strings.Contains(command, " version -json") {
			return `{"version":"0.1.0","commit":"abc123","os":"linux","arch":"amd64"}`, nil
		}
		if strings.Contains(command, "systemctl show") {
			return "User=ozal\nGroup=ozal-state\n", nil
		}
		return "", nil
	}}
	node := &pairSSHNode{remote: remote, name: "edge-edge", role: "edge", unit: "ozal.service", user: "ozal", binary: "/usr/local/lib/ozal/ozal", stage: "/home/deploy/stage", lock: "/home/deploy/lock", config: "/etc/ozal/config.json", db: "/var/lib/ozal/state.db", socket: "/run/ozal/admin.sock"}
	state, err := node.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := node.CheckPreflight(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	for _, command := range remote.commands {
		for _, forbidden := range []string{"mkdir", "rm ", "mv ", "chmod", "systemctl stop", "systemctl start"} {
			if strings.Contains(command, forbidden) {
				t.Fatalf("preflight remote command can mutate state (%q): %s", forbidden, command)
			}
		}
	}
	for _, required := range []string{"curl --silent", "test -w", "printf held"} {
		found := strings.Contains(remote.terminal, required)
		for _, command := range remote.commands {
			found = found || strings.Contains(command, required)
		}
		if !found {
			t.Errorf("preflight omitted %q", required)
		}
	}
}
