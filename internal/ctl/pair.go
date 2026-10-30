package ctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/release"
)

var pairText = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// PairState is the exact pre-update executable identity captured under the
// node lock. The adapter's checkpoint must also preserve mutable state needed
// to restore that executable safely.
type PairState struct {
	Version string
	SHA256  string
}

// PairProgress is the allowlisted report posted to the edge's host-only
// status endpoint. It contains status data only, never commands or paths.
type PairProgress struct {
	CheckedAt string             `json:"checked_at"`
	Installed string             `json:"installed"`
	Latest    string             `json:"latest"`
	Result    string             `json:"result"`
	JobID     string             `json:"job_id"`
	Phase     string             `json:"phase"`
	Nodes     []PairProgressNode `json:"nodes"`
}

type PairProgressNode struct {
	Role      string `json:"role"`
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Latest    string `json:"latest"`
	SHA256    string `json:"sha256"`
	Result    string `json:"result"`
}

// PairNode is one fixed participant in a tunnel update. Implementations bind
// each operation to configured paths and units; callers cannot supply shell
// commands or filesystem paths through this interface.
type PairNode interface {
	Name() string
	Acquire(context.Context, string) error
	Release(context.Context, string) error
	Inspect(context.Context) (PairState, error)
	Stage(context.Context, Binary, string) error
	Checkpoint(context.Context, PairState) error
	Stop(context.Context) error
	Install(context.Context, string) error
	Start(context.Context) error
	Ready(context.Context, string) error
	Restore(context.Context, PairState) error
	Report(context.Context, PairProgress) error // edge adapter reports through its local status API
}

// PairUpdateRequest coordinates an edge and connector from one committed
// candidate. The edge owns protocol acceptance; the connector is stopped
// first, and started only after the edge candidate is live.
type PairUpdateRequest struct {
	Ref   string
	Owner string
	JobID string
	Apply bool
	Force bool // allow moving from an unversioned development build
	Now   func() time.Time
}

// UpdatePair builds one candidate and transitions the pair as a unit. Both
// nodes are locked, staged, and checkpointed before either service stops.
// After cutover starts, any failure attempts a paired restore and reports
// intervention if either side cannot be recovered.
func UpdatePair(ctx context.Context, out io.Writer, builder Builder, edge, connector PairNode, req PairUpdateRequest) (retErr error) {
	if edge == nil || connector == nil || !pairText.MatchString(edge.Name()) || !pairText.MatchString(connector.Name()) || edge.Name() == connector.Name() {
		return errors.New("a distinct edge and connector are required")
	}
	if strings.TrimSpace(req.Ref) == "" || strings.TrimSpace(req.Owner) == "" || !pairText.MatchString(req.JobID) {
		return errors.New("a committed ref, update owner and job ID are required")
	}
	step := func(format string, args ...any) { fmt.Fprintf(out, "==> "+format+"\n", args...) }
	note := func(format string, args ...any) { fmt.Fprintf(out, "    "+format+"\n", args...) }
	now := req.Now
	if now == nil {
		now = time.Now
	}
	step("build shared candidate %s", req.Ref)
	bin, err := builder.Build(ctx, req.Ref)
	if err != nil {
		return err
	}
	if len(bin.Data) == 0 || !release.IsVersion(bin.Commit) || req.Ref != bin.Commit {
		return errors.New("builder returned an empty candidate or an invalid commit identifier")
	}
	want := sha(bin.Data)
	note("%s: sha256 %s… (%d bytes)", bin.Commit, want[:16], len(bin.Data))
	if !req.Apply {
		step("plan only: on apply, stage and checkpoint both nodes, stop connector then edge, install one shared candidate, start edge then connector, and verify both")
		step("participants: edge %s; connector %s", edge.Name(), connector.Name())
		return nil
	}

	locked := make([]PairNode, 0, 2)
	for _, node := range []PairNode{edge, connector} {
		if err := node.Acquire(ctx, req.Owner); err != nil {
			var releaseErrs []error
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			for i := len(locked) - 1; i >= 0; i-- {
				if releaseErr := locked[i].Release(releaseCtx, req.Owner); releaseErr != nil {
					releaseErrs = append(releaseErrs, fmt.Errorf("release lock on %s: %w", locked[i].Name(), releaseErr))
				}
			}
			cancel()
			return errors.Join(fmt.Errorf("lock %s: %w", node.Name(), err), errors.Join(releaseErrs...))
		}
		locked = append(locked, node)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for i := len(locked) - 1; i >= 0; i-- {
			if err := locked[i].Release(releaseCtx, req.Owner); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("release lock on %s: %w", locked[i].Name(), err))
			}
		}
	}()

	previous := make(map[string]PairState, 2)
	for _, node := range []PairNode{edge, connector} {
		state, err := node.Inspect(ctx)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", node.Name(), err)
		}
		if !validSHA256(state.SHA256) {
			return fmt.Errorf("%s reported an invalid installed binary hash", node.Name())
		}
		previous[node.Name()] = state
		if !release.IsVersion(state.Version) {
			if !req.Force {
				return fmt.Errorf("%s is running an unversioned build; pass -force once after reviewing the release", node.Name())
			}
		} else if comparison := release.Compare(bin.Commit, state.Version); comparison < 0 {
			return fmt.Errorf("refusing to downgrade %s from %s to %s", node.Name(), state.Version, bin.Commit)
		} else if comparison == 0 && state.SHA256 != want {
			return fmt.Errorf("release %s is immutable but %s has a different binary hash", bin.Commit, node.Name())
		}
		note("%s: installed %s sha256 %s…", node.Name(), state.Version, state.SHA256[:16])
	}
	if previous[edge.Name()].SHA256 == want && previous[connector.Name()].SHA256 == want {
		step("both nodes already run %s; nothing to do", bin.Commit)
		current := pairProgress(now, req.JobID, bin.Commit, edge, connector, previous, want, "current", "complete", "updated")
		if err := edge.Report(ctx, current); err != nil {
			return fmt.Errorf("both nodes are current but the status API did not accept the report: %w", err)
		}
		return nil
	}
	for _, node := range []PairNode{edge, connector} {
		step("stage and verify %s", node.Name())
		if err := node.Stage(ctx, bin, want); err != nil {
			return fmt.Errorf("stage %s: %w", node.Name(), err)
		}
	}
	progress := pairProgress(now, req.JobID, bin.Commit, edge, connector, previous, want, "updating", "staging", "staged")
	if err := edge.Report(ctx, progress); err != nil {
		return fmt.Errorf("publish pair update status after staging: %w", err)
	}
	for _, node := range []PairNode{edge, connector} {
		step("checkpoint %s", node.Name())
		if err := node.Checkpoint(ctx, previous[node.Name()]); err != nil {
			return fmt.Errorf("checkpoint %s: %w", node.Name(), err)
		}
	}
	progress = pairProgress(now, req.JobID, bin.Commit, edge, connector, previous, want, "updating", "checkpointing", "staged")
	if err := edge.Report(ctx, progress); err != nil {
		return fmt.Errorf("publish pair update status before stopping services: %w", err)
	}

	progress = pairProgress(now, req.JobID, bin.Commit, edge, connector, previous, want, "updating", "stopping", "staged")
	if err := edge.Report(ctx, progress); err != nil {
		return fmt.Errorf("publish pair update status before stopping services: %w", err)
	}
	step("stop connector, then edge")
	if err := connector.Stop(ctx); err != nil {
		return rollbackPair(ctx, out, edge, connector, previous, progress, now, fmt.Errorf("stop connector before cutover: %w", err))
	}
	if err := edge.Stop(ctx); err != nil {
		return rollbackPair(ctx, out, edge, connector, previous, progress, now, fmt.Errorf("stop edge: %w", err))
	}
	for _, node := range []PairNode{edge, connector} {
		step("install candidate on %s", node.Name())
		if err := node.Install(ctx, want); err != nil {
			return rollbackPair(ctx, out, edge, connector, previous, progress, now, fmt.Errorf("install %s: %w", node.Name(), err))
		}
	}
	step("start edge, then connector")
	if err := edge.Start(ctx); err != nil {
		return rollbackPair(ctx, out, edge, connector, previous, progress, now, fmt.Errorf("start edge: %w", err))
	}
	progress = pairProgress(now, req.JobID, bin.Commit, edge, connector, previous, want, "updating", "starting", "staged")
	if err := edge.Report(ctx, progress); err != nil {
		return rollbackPair(ctx, out, edge, connector, previous, progress, now, fmt.Errorf("publish pair update start status: %w", err))
	}
	if err := connector.Start(ctx); err != nil {
		return rollbackPair(ctx, out, edge, connector, previous, progress, now, fmt.Errorf("start connector: %w", err))
	}
	progress = pairProgress(now, req.JobID, bin.Commit, edge, connector, previous, want, "updating", "verifying", "staged")
	if err := edge.Report(ctx, progress); err != nil {
		return rollbackPair(ctx, out, edge, connector, previous, progress, now, fmt.Errorf("publish pair update verification status: %w", err))
	}
	for _, node := range []PairNode{edge, connector} {
		step("verify %s", node.Name())
		if err := node.Ready(ctx, want); err != nil {
			return rollbackPair(ctx, out, edge, connector, previous, progress, now, fmt.Errorf("verify %s: %w", node.Name(), err))
		}
	}
	progress = pairProgress(now, req.JobID, bin.Commit, edge, connector, previous, want, "updated", "complete", "updated")
	if err := edge.Report(ctx, progress); err != nil {
		return fmt.Errorf("both nodes are updated and ready, but the status API did not accept the final report: %w", err)
	}
	step("paired update complete: %s and %s run %s", edge.Name(), connector.Name(), bin.Commit)
	return nil
}

func pairProgress(now func() time.Time, jobID, latest string, edge, connector PairNode, previous map[string]PairState, nextHash, result, phase, nodeResult string) PairProgress {
	checkedAt := now().UTC().Format(time.RFC3339)
	installed := previous[edge.Name()].Version
	edgeInstalled := previous[edge.Name()].Version
	connectorInstalled := previous[connector.Name()].Version
	if result == "updated" {
		installed, edgeInstalled, connectorInstalled = latest, latest, latest
	}
	return PairProgress{
		CheckedAt: checkedAt, Installed: installed, Latest: latest,
		Result: result, JobID: jobID, Phase: phase,
		Nodes: []PairProgressNode{
			{Role: "edge", Name: edge.Name(), Installed: edgeInstalled, Latest: latest, SHA256: nextHash, Result: nodeResult},
			{Role: "connector", Name: connector.Name(), Installed: connectorInstalled, Latest: latest, SHA256: nextHash, Result: nodeResult},
		},
	}
}

func rollbackPair(ctx context.Context, out io.Writer, edge, connector PairNode, previous map[string]PairState, progress PairProgress, now func() time.Time, cause error) error {
	fmt.Fprintln(out, "==> paired update failed; restoring both nodes")
	var failures []error
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	if err := connector.Stop(rollbackCtx); err != nil {
		failures = append(failures, fmt.Errorf("stop connector for rollback: %w", err))
	}
	if err := edge.Stop(rollbackCtx); err != nil {
		failures = append(failures, fmt.Errorf("stop edge for rollback: %w", err))
	}
	for _, node := range []PairNode{edge, connector} {
		if err := node.Restore(rollbackCtx, previous[node.Name()]); err != nil {
			failures = append(failures, fmt.Errorf("restore %s: %w", node.Name(), err))
		}
	}
	for _, node := range []PairNode{edge, connector} {
		if err := node.Start(rollbackCtx); err != nil {
			failures = append(failures, fmt.Errorf("restart %s after rollback: %w", node.Name(), err))
		}
	}
	for _, node := range []PairNode{edge, connector} {
		if err := node.Ready(rollbackCtx, previous[node.Name()].SHA256); err != nil {
			failures = append(failures, fmt.Errorf("verify %s after rollback: %w", node.Name(), err))
		}
	}
	if len(failures) > 0 {
		progress = pairProgress(now, progress.JobID, progress.Latest, edge, connector, previous, "", "intervention_required", "rolling_back", "failed")
		_ = edge.Report(rollbackCtx, progress)
		return fmt.Errorf("%w; paired rollback requires operator intervention: %w", cause, errors.Join(failures...))
	}
	progress = pairProgress(now, progress.JobID, progress.Latest, edge, connector, previous, "", "rolled_back", "complete", "rolled_back")
	if err := edge.Report(rollbackCtx, progress); err != nil {
		fmt.Fprintf(out, "    both previous versions are ready; status report failed: %v\n", err)
	}
	fmt.Fprintln(out, "    both previous versions restored and ready")
	return fmt.Errorf("%w; paired rollback succeeded", cause)
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
