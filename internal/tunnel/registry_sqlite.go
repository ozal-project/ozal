package tunnel

import "strings"

// isConstraint reports a SQLite constraint violation without importing the
// driver here: modernc.org/sqlite error text contains "constraint failed".
func isConstraint(err error) bool {
	return err != nil && strings.Contains(err.Error(), "constraint failed")
}
