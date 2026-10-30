package pages

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNothingLive is returned when a site has no live deployment to copy.
var ErrNothingLive = errors.New("site has no live deployment")

// CopyLive makes the live deployment of one site the live deployment of
// another, for a project rename: the files are packed again and go through
// the same extraction, limits and content addressing as any upload, so the
// copy is checked like one. The source site is not changed.
func (s *Store) CopyLive(ctx context.Context, from, to string) (Deployment, error) {
	if !ValidSiteName(from) || !ValidSiteName(to) {
		return Deployment{}, ErrBadSiteName
	}
	live, ok := s.Live(from)
	if !ok {
		return Deployment{}, ErrNothingLive
	}
	reader, writer := io.Pipe()
	go func() { writer.CloseWithError(packDir(live.Dir, writer)) }()
	defer reader.Close()
	return s.deploy(ctx, to, SourceMetadata{}, reader)
}

// packDir writes dir's regular files and directories as a tar.gz with
// relative slash paths.
func packDir(dir string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil // extraction never wrote anything else
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			header.Name += "/"
		}
		header.Uname, header.Gname = "", ""
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
