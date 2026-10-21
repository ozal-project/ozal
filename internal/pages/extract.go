package pages

import (
	"archive/tar"
	"compress/flate"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
)

// MaxUploadBytes caps the compressed archive the admin API accepts.
const MaxUploadBytes = 100 << 20

// RefusedError is a deployment the core refused. Code is stable and safe to
// return to the client; nothing attacker-controlled is in the message.
type RefusedError struct {
	Code   Code
	Reason string
}

func (e *RefusedError) Error() string { return string(e.Code) + ": " + e.Reason }

// More refusal codes, raised while extracting rather than per entry.
const (
	BadArchive     Code = "archive.invalid"
	TooManyEntries Code = "archive.too_many_entries"
	TotalTooLarge  Code = "archive.too_large"
	Duplicate      Code = "archive.duplicate_entry"
	NoIndex        Code = "archive.no_index"
)

func refused(code Code, reason string) error { return &RefusedError{Code: code, Reason: reason} }

// ErrUploadInterrupted marks an upload that stopped arriving: a read deadline,
// a dropped connection or the size cap, not a malformed archive. The cause
// stays wrapped, so the handler can answer each as what it is (BUG-013).
var ErrUploadInterrupted = errors.New("upload interrupted")

// readError tells the archive's fault from the transport's. The format errors
// of gzip, flate and tar, and a stream that ends inside an entry, are the
// archive's; anything else came from below it.
func readError(err error, reason string) error {
	var corrupt flate.CorruptInputError
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, tar.ErrHeader) ||
		errors.Is(err, gzip.ErrHeader) || errors.Is(err, gzip.ErrChecksum) || errors.As(err, &corrupt) {
		return refused(BadArchive, reason)
	}
	return fmt.Errorf("%w: %w", ErrUploadInterrupted, err)
}

// copyEntry copies one entry and reports a failed write apart from a failed
// read: a full disk is the server's problem, never the archive's.
func copyEntry(dst io.Writer, src io.Reader) (n int64, readErr, writeErr error) {
	w := &watchedWriter{w: dst}
	n, err := io.Copy(w, src)
	if w.err != nil {
		return n, nil, w.err
	}
	return n, err, nil
}

type watchedWriter struct {
	w   io.Writer
	err error
}

func (w *watchedWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if err != nil && w.err == nil {
		w.err = err
	}
	return n, err
}

// Extract unpacks a tar.gz stream into dir, which must exist and be empty.
// Every header goes through CheckEntry, and every write goes through an
// os.Root opened on dir: even if the path check had a bug, the kernel-level
// resolution of os.Root would still refuse to leave dir. Sizes are counted
// from the bytes actually copied, never from header claims.
func Extract(r io.Reader, dir string, lim Limits) (Manifest, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Manifest{}, fmt.Errorf("open staging directory: %w", err)
	}
	defer root.Close()

	gz, err := gzip.NewReader(r)
	if errors.Is(err, io.EOF) {
		return Manifest{}, refused(BadArchive, "upload is not a gzip stream")
	}
	if err != nil {
		return Manifest{}, readError(err, "upload is not a gzip stream")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var manifest Manifest
	entries := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, readError(err, "upload is not a valid tar archive")
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue // pax global header: metadata only, no file
		}
		entries++
		if entries > lim.MaxEntries {
			return Manifest{}, refused(TooManyEntries, "archive has more entries than the limit")
		}

		decision := CheckEntry(hdr.Name, kindOf(hdr.Typeflag), hdr.Size, lim)
		if !decision.Allowed {
			return Manifest{}, &RefusedError{Code: decision.Code, Reason: decision.Reason}
		}
		if decision.Path == "" {
			continue // the site root itself
		}
		native := filepath.FromSlash(decision.Path)
		if hdr.Typeflag == tar.TypeDir {
			if err := root.MkdirAll(native, 0o755); err != nil {
				return Manifest{}, fmt.Errorf("create directory: %w", err)
			}
			continue
		}

		if parent := path.Dir(decision.Path); parent != "." {
			if err := root.MkdirAll(filepath.FromSlash(parent), 0o755); err != nil {
				return Manifest{}, fmt.Errorf("create directory: %w", err)
			}
		}
		// O_EXCL: a second entry with the same name is refused instead of
		// silently replacing the first one.
		out, err := root.OpenFile(native, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			return Manifest{}, refused(Duplicate, "archive contains the same path twice")
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("create file: %w", err)
		}
		hash := sha256.New()
		// Read at most one byte past what is still allowed, so an overflow is
		// detected without ever writing it all.
		budget := min(lim.MaxFileBytes, lim.MaxTotalBytes-manifest.Bytes)
		n, readErr, writeErr := copyEntry(io.MultiWriter(out, hash), io.LimitReader(tr, budget+1))
		closeErr := out.Close()
		if writeErr != nil {
			return Manifest{}, fmt.Errorf("write file: %w", writeErr)
		}
		if readErr != nil {
			return Manifest{}, readError(readErr, "archive ended inside a file")
		}
		if closeErr != nil {
			return Manifest{}, fmt.Errorf("write file: %w", closeErr)
		}
		if n > budget {
			if n > lim.MaxFileBytes {
				return Manifest{}, refused(TooLarge, "entry is larger than the per-file limit")
			}
			return Manifest{}, refused(TotalTooLarge, "archive expands beyond the total limit")
		}
		manifest.Bytes += n
		manifest.Files = append(manifest.Files, File{Path: decision.Path, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: n})
	}

	if !hasIndex(manifest.Files) {
		return Manifest{}, refused(NoIndex, "archive has no index.html at its root")
	}
	return manifest, nil
}

// kindOf maps a tar typeflag to the core's entry kind.
func kindOf(flag byte) Kind {
	switch flag {
	case tar.TypeReg:
		return KindFile
	case tar.TypeDir:
		return KindDir
	case tar.TypeSymlink:
		return KindSymlink
	case tar.TypeLink:
		return KindHardlink
	default:
		return KindOther
	}
}

func hasIndex(files []File) bool {
	for _, f := range files {
		if f.Path == "index.html" {
			return true
		}
	}
	return false
}

// RulesInvalid refuses a deployment whose _redirects or _headers does not
// parse: a typo must fail the deploy, not silently drop a security header.
const RulesInvalid Code = "rules.invalid"

// Rule files at the deployment root. The static handler never serves them.
const (
	RedirectsFile = "_redirects"
	HeadersFile   = "_headers"
)

// LoadRules reads and parses a deployment's rule files; a missing file has
// no rules. Reads go through os.Root and are capped like the parser's limit.
func LoadRules(dir string) (*Rules, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open deployment: %w", err)
	}
	defer root.Close()
	read := func(name string) ([]byte, error) {
		f, err := root.Open(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(io.LimitReader(f, MaxRulesFileBytes+1))
	}
	redirects, err := read(RedirectsFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", RedirectsFile, err)
	}
	headers, err := read(HeadersFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", HeadersFile, err)
	}
	rules, err := ParseRules(redirects, headers)
	var invalid *RulesError
	if errors.As(err, &invalid) {
		return nil, refused(RulesInvalid, invalid.Error())
	}
	return rules, err
}
