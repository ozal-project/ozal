package pages

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
)

// BUG-013: an upload that stops arriving, and a disk that refuses a write,
// are not malformed archives.

// cutReader serves the first n bytes of data, then fails with err.
type cutReader struct {
	data []byte
	n    int
	err  error
}

func (c *cutReader) Read(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, c.err
	}
	k := copy(p, c.data[:min(len(c.data), c.n)])
	c.data, c.n = c.data[k:], c.n-k
	return k, nil
}

// bigArchive has an index and one file large enough that a cut halfway lands
// inside it; random hex does not compress away.
func bigArchive(t *testing.T) []byte {
	t.Helper()
	noise := make([]byte, 100<<10)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	return archive(t, entry{name: "index.html", body: "<h1>hi</h1>"}, entry{name: "big.txt", body: hex.EncodeToString(noise)}).Bytes()
}

func TestAnUploadCutByADeadlineIsNotABadArchive(t *testing.T) {
	data := bigArchive(t)
	_, err := Extract(&cutReader{data: data, n: len(data) / 2, err: os.ErrDeadlineExceeded}, t.TempDir(), DefaultLimits)
	var refusal *RefusedError
	if errors.As(err, &refusal) {
		t.Fatalf("a read deadline was reported as a refusal: %v", err)
	}
	if !errors.Is(err, ErrUploadInterrupted) || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want ErrUploadInterrupted wrapping the deadline", err)
	}
}

func TestAnUploadOverTheCapKeepsItsCause(t *testing.T) {
	data := bigArchive(t)
	body := http.MaxBytesReader(nil, io.NopCloser(bytes.NewReader(data)), int64(len(data)/2))
	_, err := Extract(body, t.TempDir(), DefaultLimits)
	var tooBig *http.MaxBytesError
	if !errors.As(err, &tooBig) {
		t.Fatalf("err = %v, want the MaxBytesError for the handler's 413", err)
	}
}

func TestATruncatedArchiveIsStillRefused(t *testing.T) {
	data := bigArchive(t)
	_, err := Extract(bytes.NewReader(data[:len(data)/2]), t.TempDir(), DefaultLimits)
	var refusal *RefusedError
	if !errors.As(err, &refusal) || refusal.Code != BadArchive {
		t.Fatalf("err = %v, want refusal %s: the archive itself ends early", err, BadArchive)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

func TestCopyEntryBlamesTheWriteNotTheArchive(t *testing.T) {
	_, readErr, writeErr := copyEntry(failingWriter{}, bytes.NewReader([]byte("content")))
	if readErr != nil || writeErr == nil {
		t.Fatalf("readErr = %v, writeErr = %v: a failed write must be reported as a write", readErr, writeErr)
	}
	_, readErr, writeErr = copyEntry(io.Discard, &cutReader{data: []byte("abc"), n: 2, err: os.ErrDeadlineExceeded})
	if writeErr != nil || !errors.Is(readErr, os.ErrDeadlineExceeded) {
		t.Fatalf("readErr = %v, writeErr = %v: a failed read must be reported as a read", readErr, writeErr)
	}
}
