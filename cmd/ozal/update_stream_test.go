package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/diskspace"
)

type archiveStream struct {
	left    int64
	maxRead int
}

func (r *archiveStream) Read(p []byte) (int, error) {
	if len(p) > r.maxRead {
		r.maxRead = len(p)
	}
	if r.left == 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), r.left))
	for i := range p[:n] {
		p[i] = 'x'
	}
	r.left -= int64(n)
	return n, nil
}

type archiveSink struct {
	source      *archiveStream
	bytes       int64
	incremental bool
}

func (w *archiveSink) Write(p []byte) (int, error) {
	if w.bytes == 0 {
		w.incremental = w.source.left > 0
	}
	w.bytes += int64(len(p))
	return len(p), nil
}

type fullDisk struct{}

func (fullDisk) Write([]byte) (int, error) { return 0, syscall.ENOSPC }

func TestMaterializeWritesToDiskBeforeTheResponseFinishes(t *testing.T) {
	body := tarGz(t, "ozal", []byte("verified binary"))
	sum := sha256.Sum256(body)
	release := make(chan struct{})
	var once sync.Once
	finishResponse := func() { once.Do(func() { close(release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body[:len(body)/2])
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(body[len(body)/2:])
	}))
	defer srv.Close()
	defer finishResponse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	work := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, _, err := materialize(ctx, srv.Client(), updateCandidate{ArchiveURL: srv.URL, ArchiveSHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}, work, func(context.Context, ...diskspace.Need) error { return nil })
		done <- err
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("download ended before the response was released: %v", err)
		case <-ctx.Done():
			finishResponse()
			<-done
			t.Fatal("no archive bytes reached disk before the HTTP response finished")
		case <-tick.C:
			if info, err := os.Stat(filepath.Join(work, "archive.tar.gz")); err == nil && info.Size() > 0 {
				finishResponse()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				return
			}
		}
	}
}

func TestArchiveCopyWritesIncrementallyWithBoundedReads(t *testing.T) {
	r := &archiveStream{left: 8 << 20}
	w := &archiveSink{source: r}
	sum, n, err := copyArchive(w, r, 8<<20)
	if err != nil || n != 8<<20 || len(sum) != 64 || !w.incremental || r.maxRead > 32<<10 {
		t.Fatalf("n=%d err=%v incremental=%v maxRead=%d", n, err, w.incremental, r.maxRead)
	}
	_, n, err = copyArchive(io.Discard, strings.NewReader("too many bytes"), 3)
	if err == nil || n != 4 {
		t.Fatalf("oversized response n=%d err=%v", n, err)
	}
	if _, _, err := copyArchive(fullDisk{}, strings.NewReader("x"), 8); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("disk error=%v", err)
	}
}

func TestMaterializeCleansFailedDownloadsAndRefusesBeforeExecution(t *testing.T) {
	for _, tc := range []struct {
		name       string
		badHash    bool
		shortBody  bool
		badArchive bool
		oversize   bool
		spaceFail  int
	}{
		{name: "hash changed", badHash: true},
		{name: "connection cut short", shortBody: true},
		{name: "authenticated truncated archive", badArchive: true},
		{name: "larger than metadata", oversize: true},
		{name: "no space before download", spaceFail: 1},
		{name: "no space for extracted binary", spaceFail: 2},
		{name: "valid archive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tarGz(t, "ozal", []byte("verified binary"))
			if tc.badArchive {
				body = body[:len(body)/2]
			}
			hash := sha256.Sum256(body)
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				if tc.shortBody {
					w.Header().Set("Content-Length", "1000000")
					_, _ = w.Write(body[:len(body)/2])
					return
				}
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			candidate := updateCandidate{ArchiveURL: srv.URL, ArchiveSHA256: hex.EncodeToString(hash[:]), Size: int64(len(body))}
			if tc.badHash {
				candidate.ArchiveSHA256 = strings.Repeat("0", 64)
			}
			if tc.oversize {
				candidate.Size = 1
			}
			calls := 0
			space := func(context.Context, ...diskspace.Need) error {
				calls++
				if calls == tc.spaceFail {
					return diskspace.ErrInsufficient
				}
				return nil
			}
			work := t.TempDir()
			path, sum, err := materialize(context.Background(), srv.Client(), candidate, work, space)
			entries, readErr := os.ReadDir(work)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.name == "valid archive" {
				data, readErr := os.ReadFile(path)
				want := sha256.Sum256([]byte("verified binary"))
				if err != nil || readErr != nil || !bytes.Equal(data, []byte("verified binary")) || sum != hex.EncodeToString(want[:]) || len(entries) != 1 {
					t.Fatalf("path=%s sum=%s err=%v read=%v entries=%v", path, sum, err, readErr, entries)
				}
			} else if err == nil || path != "" || len(entries) != 0 {
				t.Fatalf("failed materialization path=%s err=%v leftovers=%v", path, err, entries)
			}
			if tc.spaceFail == 1 && hits.Load() != 0 {
				t.Fatal("archive downloaded despite insufficient staging space")
			}
		})
	}
}

func TestUpdateStorageOrVersionRefusalNeverStartsAnUpgrade(t *testing.T) {
	for _, name := range []string{"staging full", "installation full", "wrong embedded version"} {
		t.Run(name, func(t *testing.T) {
			r := newUpdateRig(t, "0.1.0")
			r.host.publish(t, r.srv.URL, "v0.2.0", r.key, []byte("candidate"), false)
			if name == "staging full" {
				r.spaceCheck = func(context.Context, ...diskspace.Need) error { return diskspace.ErrInsufficient }
			} else if name == "installation full" {
				r.prepareErr = diskspace.ErrInsufficient
			} else {
				r.versionErr = errors.New("candidate version mismatch")
			}
			if _, err := r.run(t, "-apply"); err == nil || len(r.upgrades) != 0 {
				t.Fatalf("err=%v upgrades=%d", err, len(r.upgrades))
			}
			if name == "staging full" && r.archiveHits("v0.2.0") != 0 {
				t.Fatal("archive requested on a full filesystem")
			}
		})
	}
}

func TestUpdateParentDoesNotTrustCandidateSuccessOrRollbackWithoutProcessProof(t *testing.T) {
	for _, tc := range []struct {
		name string
		exit error
		old  bool
	}{
		{"candidate claims success", nil, false},
		{"candidate claims rollback", exitStatusError(3), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newUpdateRig(t, "0.1.0")
			r.host.publish(t, r.srv.URL, "v0.2.0", r.key, []byte("candidate"), false)
			r.failWith = tc.exit
			r.currentErr = errors.New("the image is not running")
			_, err := r.run(t, "-apply")
			if err == nil || len(r.upgrades) != 1 {
				t.Fatalf("err=%v upgrades=%d", err, len(r.upgrades))
			}
			if s := r.readStatus(t); s.Result != "intervention_required" {
				t.Fatalf("unverified candidate claim reported %+v", s)
			}
			want := strings.Repeat("a", 64)
			if !tc.old {
				sum := sha256.Sum256([]byte("candidate"))
				want = hex.EncodeToString(sum[:])
			}
			if len(r.checkedImages) != 1 || r.checkedImages[0] != want {
				t.Fatalf("verified images=%v want %s", r.checkedImages, want)
			}
			if result, _ := updateJobOutcome("intervention_required", "unverified", err); result != "intervention_required" {
				t.Fatalf("journal result=%s", result)
			}
		})
	}
}

func TestCurrentApplyRequiresProofOfTheRunningProcess(t *testing.T) {
	r := newUpdateRig(t, "0.2.0")
	r.host.publish(t, r.srv.URL, "v0.2.0", r.key, []byte("installed candidate"), false)
	r.currentErr = errors.New("active process is still the old version")
	if _, err := r.run(t); err != nil {
		t.Fatalf("metadata-only check unexpectedly required host access: %v", err)
	}
	if _, err := r.run(t, "-apply"); !errors.Is(err, r.currentErr) {
		t.Fatalf("apply accepted an unverified process: %v", err)
	}
	if len(r.upgrades) != 0 || r.archiveHits("v0.2.0") != 0 {
		t.Fatal("current check attempted an install or download")
	}
	if state := r.readStatus(t); state.Result != "failed" {
		t.Fatalf("unverified current process reported %+v", state)
	}
}
