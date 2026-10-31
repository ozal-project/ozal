package diskspace

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestBudgetsShareCapacityAndReserveOnlyOnTheSameFilesystem(t *testing.T) {
	for _, tc := range []struct {
		name      string
		separate  bool
		available uint64
		wantError bool
	}{
		{"shared filesystem cannot afford both writes", false, uint64(Reserve) + 70, true},
		{"separate filesystems can each afford a write", true, uint64(Reserve) + 50, false},
		{"one reserve per filesystem at exact capacity", false, uint64(Reserve) + 80, false},
		{"reserve must remain available", true, uint64(Reserve) - 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := func(_ context.Context, path string) (Volume, error) {
				id := uint64(1)
				if tc.separate && path == "db" {
					id = 2
				}
				return Volume{ID: id, Available: tc.available}, nil
			}
			err := check(context.Background(), probe, Need{Dir: "binary", Bytes: 40}, Need{Dir: "db", Bytes: 40})
			if errors.Is(err, ErrInsufficient) != tc.wantError {
				t.Fatalf("err=%v want insufficient=%v", err, tc.wantError)
			}
		})
	}
}

func TestCapacityFailuresAndCancellationRefuseTheBudget(t *testing.T) {
	failure := errors.New("filesystem query failed")
	if err := check(context.Background(), func(context.Context, string) (Volume, error) { return Volume{}, failure }, Need{Dir: "x", Bytes: 1}); !errors.Is(err, failure) {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := check(ctx, func(context.Context, string) (Volume, error) {
		t.Fatal("cancelled check queried storage")
		return Volume{}, nil
	}, Need{Dir: "x", Bytes: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	probe := func(context.Context, string) (Volume, error) { return Volume{ID: 1, Available: math.MaxUint64}, nil }
	if err := check(context.Background(), probe, Need{Bytes: -1}); err == nil {
		t.Fatal("negative budget accepted")
	}
	if err := check(context.Background(), probe, Need{Bytes: math.MaxInt64}, Need{Bytes: math.MaxInt64}); err == nil {
		t.Fatal("overflowing budget accepted")
	}
}
