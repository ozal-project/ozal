//go:build !linux

package diskspace

import (
	"context"
	"errors"
)

func probe(context.Context, string) (Volume, error) {
	return Volume{}, errors.New("update space checks need Linux")
}
