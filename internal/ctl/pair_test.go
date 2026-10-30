package ctl

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type pairBuilder struct{ bin Binary }

func (b pairBuilder) Build(context.Context, string) (Binary, error) { return b.bin, nil }

type pairNodeStub struct {
	name      string
	trace     *[]string
	state     PairState
	fail      map[string]error
	candidate string
}

func (n *pairNodeStub) record(action string) error {
	*n.trace = append(*n.trace, n.name+"."+action)
	return n.fail[action]
}
func (n *pairNodeStub) Name() string                          { return n.name }
func (n *pairNodeStub) Acquire(context.Context, string) error { return n.record("acquire") }
func (n *pairNodeStub) Release(context.Context, string) error { return n.record("release") }
func (n *pairNodeStub) Inspect(context.Context) (PairState, error) {
	return n.state, n.record("inspect")
}
func (n *pairNodeStub) CheckPreflight(_ context.Context, state PairState) error {
	if state != n.state {
		return errors.New("preflight received a different inspection")
	}
	return n.record("preflight")
}
func (n *pairNodeStub) Stage(_ context.Context, bin Binary, want string) error {
	n.candidate = want
	if want != sha(bin.Data) {
		return errors.New("bad candidate hash")
	}
	return n.record("stage")
}
func (n *pairNodeStub) Checkpoint(context.Context, PairState) error { return n.record("checkpoint") }
func (n *pairNodeStub) Stop(context.Context) error                  { return n.record("stop") }
func (n *pairNodeStub) Install(context.Context, string) error       { return n.record("install") }
func (n *pairNodeStub) Start(context.Context) error                 { return n.record("start") }
func (n *pairNodeStub) Ready(context.Context, string) error         { return n.record("ready") }
func (n *pairNodeStub) Restore(context.Context, PairState) error    { return n.record("restore") }
func (n *pairNodeStub) Report(_ context.Context, progress PairProgress) error {
	return n.record("report-" + progress.Result + "-" + progress.Phase)
}

func pairFixture() (*pairNodeStub, *pairNodeStub, *[]string, Binary) {
	trace := []string{}
	bin := Binary{Data: []byte("candidate"), Commit: "v0.2.0"}
	edge := &pairNodeStub{name: "edge", trace: &trace, state: PairState{Version: "0.1.0", SHA256: strings.Repeat("a", 64)}, fail: map[string]error{}}
	connector := &pairNodeStub{name: "connector", trace: &trace, state: PairState{Version: "0.1.0", SHA256: strings.Repeat("b", 64)}, fail: map[string]error{}}
	return edge, connector, &trace, bin
}

func TestUpdatePairPlanDoesNotContactNodes(t *testing.T) {
	edge, connector, trace, bin := pairFixture()
	if err := UpdatePair(context.Background(), io.Discard, pairBuilder{bin}, edge, connector, PairUpdateRequest{Ref: "v0.2.0", Owner: "operator", JobID: "job-1", Apply: false}); err != nil {
		t.Fatal(err)
	}
	if len(*trace) != 0 {
		t.Fatalf("plan changed remote state: %v", *trace)
	}
}

func TestPreflightPairInspectsBothNodesWithoutAcquiringLocks(t *testing.T) {
	edge, connector, trace, _ := pairFixture()
	edge.state.ServiceUser, edge.state.ServiceGroup = "ozal", "ozal"
	var output strings.Builder
	if err := PreflightPair(context.Background(), &output, edge, connector); err != nil {
		t.Fatal(err)
	}
	want := []string{"edge.inspect", "edge.preflight", "connector.inspect", "connector.preflight"}
	if strings.Join(*trace, ",") != strings.Join(want, ",") {
		t.Fatalf("preflight called %v, want %v", *trace, want)
	}
	if !strings.Contains(output.String(), "read-only preflight passed") {
		t.Fatalf("preflight summary missing: %s", output.String())
	}
}

func TestPreflightPairRejectsInvalidInspectionBeforeCheckingNextNode(t *testing.T) {
	edge, connector, trace, _ := pairFixture()
	edge.state.SHA256 = "invalid"
	if err := PreflightPair(context.Background(), io.Discard, edge, connector); err == nil {
		t.Fatal("invalid hash passed preflight")
	}
	if strings.Join(*trace, ",") != "edge.inspect" {
		t.Fatalf("preflight continued after invalid edge hash: %v", *trace)
	}
}

func TestUpdatePairStagesAndCheckpointsBothBeforeStoppingEither(t *testing.T) {
	edge, connector, trace, bin := pairFixture()
	err := UpdatePair(context.Background(), io.Discard, pairBuilder{bin}, edge, connector, PairUpdateRequest{Ref: "v0.2.0", Owner: "operator", JobID: "job-1", Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"edge.acquire", "connector.acquire", "edge.inspect", "connector.inspect",
		"edge.stage", "connector.stage", "edge.report-updating-staging",
		"edge.checkpoint", "connector.checkpoint", "edge.report-updating-checkpointing", "edge.report-updating-stopping",
		"connector.stop", "edge.stop", "edge.install", "connector.install",
		"edge.start", "edge.report-updating-starting", "connector.start", "edge.report-updating-verifying",
		"edge.ready", "connector.ready", "edge.report-updated-complete",
		"connector.release", "edge.release",
	}
	if strings.Join(*trace, ",") != strings.Join(want, ",") {
		t.Fatalf("sequence = %v, want %v", *trace, want)
	}
	if edge.candidate != connector.candidate || edge.candidate != sha(bin.Data) {
		t.Fatal("nodes did not receive the exact same candidate hash")
	}
}

func TestUpdatePairRestoresBothWhenOneInstallFails(t *testing.T) {
	edge, connector, trace, bin := pairFixture()
	cause := errors.New("connector replacement failed")
	connector.fail["install"] = cause
	err := UpdatePair(context.Background(), io.Discard, pairBuilder{bin}, edge, connector, PairUpdateRequest{Ref: "v0.2.0", Owner: "operator", JobID: "job-1", Apply: true})
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "paired rollback succeeded") {
		t.Fatalf("error = %v, want install failure and successful rollback", err)
	}
	joined := strings.Join(*trace, ",")
	for _, want := range []string{"connector.stop", "edge.stop", "edge.restore", "connector.restore", "edge.start", "connector.start", "edge.ready", "connector.ready"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rollback trace %v lacks %s", *trace, want)
		}
	}
}

func TestUpdatePairReportsInterventionWhenRollbackFails(t *testing.T) {
	edge, connector, _, bin := pairFixture()
	cause := errors.New("connector replacement failed")
	connector.fail["install"] = cause
	edge.fail["restore"] = errors.New("checkpoint unavailable")
	err := UpdatePair(context.Background(), io.Discard, pairBuilder{bin}, edge, connector, PairUpdateRequest{Ref: "v0.2.0", Owner: "operator", JobID: "job-1", Apply: true})
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "operator intervention") {
		t.Fatalf("error = %v, want intervention-required rollback result", err)
	}
}

func TestUpdatePairRefusesDowngradeAndUnversionedBuildWithoutForce(t *testing.T) {
	for _, test := range []struct {
		name      string
		version   string
		candidate string
	}{
		{name: "downgrade", version: "v0.3.0", candidate: "v0.2.0"},
		{name: "unversioned", version: "dev-build-a1b2c3", candidate: "v0.2.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			edge, connector, trace, _ := pairFixture()
			edge.state.Version = test.version
			connector.state.Version = test.version
			builder := pairBuilder{Binary{Data: []byte("candidate"), Commit: test.candidate}}
			err := UpdatePair(context.Background(), io.Discard, builder, edge, connector, PairUpdateRequest{Ref: test.candidate, Owner: "operator", JobID: "job-1", Apply: true})
			if err == nil {
				t.Fatal("unsafe version transition accepted")
			}
			for _, action := range *trace {
				if strings.Contains(action, ".stage") || strings.Contains(action, ".stop") || strings.Contains(action, ".install") {
					t.Fatalf("unsafe transition touched a node: %v", *trace)
				}
			}
		})
	}
}
