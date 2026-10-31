package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/ozal-project/ozal/internal/diskspace"
	"github.com/ozal-project/ozal/internal/upgrade"
)

// The parent checks storage even when the candidate's own upgrade command is
// older than these checks, and retains the original image digest for rollback.
func prepareUpdateStorage(ctx context.Context, source, binary, db string) (string, error) {
	if err := upgrade.CheckSpace(ctx, upgrade.Plan{Source: source, Binary: binary, DB: db}, diskspace.Check); err != nil {
		return "", err
	}
	return installedImageHash(ctx, binary)
}

func installedImageHash(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("installed image is not a regular file")
	}
	return imageSHA256(ctx, f)
}

func imageSHA256(ctx context.Context, r io.Reader) (string, error) {
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	var count int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := r.Read(buffer)
		count += int64(n)
		if count > maxBinary {
			return "", errors.New("process image exceeds the update size limit")
		}
		_, _ = hash.Write(buffer[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
