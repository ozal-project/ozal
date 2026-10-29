package pages

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Pack builds the tar.gz of a build directory. It refuses symlinks and
// special files in the source instead of following them: the archive must
// contain exactly what the directory holds, nothing it points to.
func Pack(dir string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&archiveBuffer{buffer: &buf})
	tw := tar.NewWriter(gz)
	var total int64
	entries := 0
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		name := filepath.ToSlash(rel)
		entries++
		if entries > DefaultLimits.MaxEntries {
			return fmt.Errorf("too many deployment entries")
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink; deployments contain regular files only", name)
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755})
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", name)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > DefaultLimits.MaxFileBytes || info.Size() > DefaultLimits.MaxTotalBytes-total {
			return fmt.Errorf("deployment content exceeds size limits")
		}
		total += info.Size()
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: info.Size()}); err != nil {
			return err
		}
		f, err := root.Open(rel)
		if err != nil {
			return err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			return fmt.Errorf("deployment source changed during packing")
		}
		n, err := io.Copy(tw, io.LimitReader(f, info.Size()+1))
		if n != info.Size() {
			return fmt.Errorf("deployment source changed size during packing")
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", dir, err)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > MaxUploadBytes {
		return nil, fmt.Errorf("pack %s: archive is %d bytes, over the %d-byte upload cap", dir, buf.Len(), MaxUploadBytes)
	}
	return buf.Bytes(), nil
}

type archiveBuffer struct{ buffer *bytes.Buffer }

func (w *archiveBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxUploadBytes-w.buffer.Len() {
		return 0, fmt.Errorf("archive exceeds upload limit")
	}
	return w.buffer.Write(p)
}
