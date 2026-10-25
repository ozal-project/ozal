// Package routing contains deterministic decisions for selecting a route's
// upstream. Network probes and request forwarding live in adapter packages.
package routing

// Select returns the first eligible index at or after start, wrapping once
// across count candidates. It does not retain state; the caller advances start
// to one past the returned index to implement round-robin selection.
func Select(start, count int, eligible func(index int) bool) (int, bool) {
	if count <= 0 || eligible == nil {
		return -1, false
	}
	start %= count
	if start < 0 {
		start += count
	}
	index := start
	for range count {
		if eligible(index) {
			return index, true
		}
		index++
		if index == count {
			index = 0
		}
	}
	return -1, false
}
