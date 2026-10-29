// Package install owns installation plans and filesystem/service adapters.
package install

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MaxBinaryBytes int64 = 512 << 20

type BinaryRequest struct {
	Archive, SHA256, Destination string
	Apply                        bool
}

func validChecksum(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// Binary verifies a private archive snapshot before extracting exactly one
// regular root member. A hard link publishes the result without overwriting
// an existing file, directory or symlink, including a concurrent creation.
func Binary(out io.Writer, r BinaryRequest) error {
	if !validChecksum(r.SHA256) || r.Archive == "" || r.Destination == "" {
		return errors.New("archive, trusted SHA-256 and new destination are required")
	}
	if _, err := os.Lstat(r.Destination); !os.IsNotExist(err) {
		return errors.New("binary destination already exists or cannot be inspected")
	}
	parent := filepath.Dir(r.Destination)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return errors.New("create the destination parent before installing")
	}
	fmt.Fprintf(out, "Install verified archive member ozal at %s (apply=%t).\n", r.Destination, r.Apply)
	if !r.Apply {
		return nil
	}
	info, err := os.Lstat(r.Archive)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxBinaryBytes {
		return errors.New("archive must be a bounded regular file, not a symlink")
	}
	source, err := os.Open(r.Archive)
	if err != nil {
		return errors.New("cannot open archive")
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("archive changed during review")
	}
	work, err := os.MkdirTemp(parent, ".ozal-install-")
	if err != nil {
		return errors.New("cannot prepare private installation")
	}
	defer os.RemoveAll(work)
	snapshot, err := os.OpenFile(filepath.Join(work, "archive"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer snapshot.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(snapshot, hash), io.LimitReader(source, MaxBinaryBytes+1))
	if err != nil || n > MaxBinaryBytes {
		return errors.New("archive cannot be copied within its size limit")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), r.SHA256) {
		return errors.New("archive checksum mismatch; nothing installed")
	}
	if _, err := snapshot.Seek(0, io.SeekStart); err != nil {
		return err
	}
	gz, err := gzip.NewReader(snapshot)
	if err != nil {
		return errors.New("archive is not gzip")
	}
	defer gz.Close()
	bounded := &io.LimitedReader{R: gz, N: 2*MaxBinaryBytes + 1}
	reader := tar.NewReader(bounded)
	output, err := os.OpenFile(filepath.Join(work, "ozal"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
	}()
	count, entries := 0, 0
	for {
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return errors.New("invalid or oversized release archive")
		}
		entries++
		if entries > 20000 {
			return errors.New("release archive has too many entries")
		}
		if header.Name != "ozal" && header.Name != "./ozal" {
			continue
		}
		count++
		if count != 1 || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > MaxBinaryBytes {
			return errors.New("release needs exactly one bounded regular ozal member")
		}
		if copied, e := io.Copy(output, io.LimitReader(reader, header.Size+1)); e != nil || copied != header.Size {
			return errors.New("release binary is truncated")
		}
	}
	if _, err := io.Copy(io.Discard, bounded); err != nil {
		return errors.New("release gzip is corrupt or truncated")
	}
	if count != 1 || bounded.N <= 0 {
		return errors.New("release needs exactly one regular ozal member within the archive limit")
	}
	if err := output.Chmod(0755); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Link(filepath.Join(work, "ozal"), r.Destination); err != nil {
		return errors.New("binary destination was not created; it may already exist")
	}
	return nil
}
