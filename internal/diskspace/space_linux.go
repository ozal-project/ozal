//go:build linux

package diskspace

import (
	"context"
	"errors"
	"math"
	"syscall"
)

func probe(ctx context.Context, dir string) (Volume, error) {
	if err := ctx.Err(); err != nil {
		return Volume{}, err
	}
	var file syscall.Stat_t
	if err := syscall.Stat(dir, &file); err != nil {
		return Volume{}, err
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err != nil {
		return Volume{}, err
	}
	if fs.Bsize <= 0 || uint64(fs.Bavail) > math.MaxUint64/uint64(fs.Bsize) {
		return Volume{}, errors.New("filesystem reports an invalid free-space count")
	}
	return Volume{ID: uint64(file.Dev), Available: uint64(fs.Bavail) * uint64(fs.Bsize)}, nil
}
