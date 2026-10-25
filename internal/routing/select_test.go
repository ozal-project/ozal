package routing

import "testing"

func TestSelectSkipsUnhealthyAndWraps(t *testing.T) {
	healthy := []bool{false, true, false, true}
	tests := []struct {
		name  string
		start int
		want  int
	}{
		{name: "first eligible after start", start: 0, want: 1},
		{name: "start after the last eligible wraps", start: 2, want: 3},
		{name: "negative start normalizes", start: -1, want: 3},
		{name: "large start normalizes", start: 9, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := Select(test.start, len(healthy), func(index int) bool { return healthy[index] })
			if !ok || got != test.want {
				t.Fatalf("Select(%d) = (%d, %t), want (%d, true)", test.start, got, ok, test.want)
			}
		})
	}
}

func TestSelectNoEligibleCandidate(t *testing.T) {
	for _, test := range []struct {
		name     string
		count    int
		eligible func(int) bool
	}{
		{name: "empty", count: 0, eligible: func(int) bool { return true }},
		{name: "negative count", count: -1, eligible: func(int) bool { return true }},
		{name: "nil predicate", count: 2},
		{name: "all unhealthy", count: 2, eligible: func(int) bool { return false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, ok := Select(0, test.count, test.eligible); ok || got != -1 {
				t.Fatalf("Select = (%d, %t), want (-1, false)", got, ok)
			}
		})
	}
}
