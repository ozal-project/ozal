// Package diskspace checks incremental storage budgets before update writes.
// It is a host adapter; checking does not reserve blocks against other writers.
package diskspace

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// Reserve is left available on every filesystem after the planned writes.
const Reserve int64 = 64 << 20

var ErrInsufficient = errors.New("insufficient free space for the update")

// Need describes additional bytes at an existing directory. Existing files
// already consume space and must not be counted again.
type Need struct {
	Dir   string
	Bytes int64
}

type Volume struct {
	ID        uint64
	Available uint64
}

// Check groups needs by filesystem, including paths that share a mount, and
// keeps a fixed safety reserve. A failed capacity query refuses the update.
func Check(ctx context.Context, needs ...Need) error {
	return check(ctx, probe, needs...)
}

func check(ctx context.Context, stat func(context.Context, string) (Volume, error), needs ...Need) error {
	totals := map[uint64]uint64{}
	available := map[uint64]uint64{}
	for _, need := range needs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if need.Bytes < 0 {
			return errors.New("update storage budget cannot be negative")
		}
		v, err := stat(ctx, need.Dir)
		if err != nil {
			return fmt.Errorf("check update space at %s: %w", need.Dir, err)
		}
		if _, ok := totals[v.ID]; !ok {
			totals[v.ID] = uint64(Reserve)
			available[v.ID] = v.Available
		} else if v.Available < available[v.ID] {
			available[v.ID] = v.Available
		}
		if math.MaxUint64-totals[v.ID] < uint64(need.Bytes) {
			return errors.New("update storage budget overflows")
		}
		totals[v.ID] += uint64(need.Bytes)
		if totals[v.ID] > available[v.ID] {
			return fmt.Errorf("%w at %s: need %d bytes including reserve, available %d", ErrInsufficient, need.Dir, totals[v.ID], available[v.ID])
		}
	}
	return nil
}
