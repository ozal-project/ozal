//go:build !linux

package tunnelrenew

import "errors"

func acquireLock(string) (func(), error) {
	return nil, errors.New("automatic pilot renewal requires Linux")
}
func syncDirectory(string) error { return nil }
