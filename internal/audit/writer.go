// Package audit writes a local, append-only JSONL chain. The chain detects
// changes within the retained files; without an externally retained signed
// checkpoint it does not prove that the operator did not rewrite the whole log.
package audit

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/policycontext"
)

const (
	Version            = 1
	DefaultSegmentSize = 16 << 20
	MaxRecordBytes     = 1 << 20
	segmentPrefix      = "audit-"
	segmentSuffix      = ".jsonl"
	hashDomain         = "ozal-audit-v1\x00"
	appendQueueSize    = 256
	appendBatchMax     = 64
	appendBatchWindow  = 250 * time.Microsecond
)

const zeroHash = "0000000000000000000000000000000000000000000000000000000000000000"

var ErrClosed = errors.New("audit writer is closed")
var ErrDirectoryLocked = errors.New("audit directory is already in use")
var ErrAppendQueueFull = errors.New("audit append queue is full")
var ErrUnsupportedPlatform = errors.New("owner-only audit storage is not supported on this platform yet")

// ErrIncompleteTail identifies a journal whose final record lacks its newline
// terminator. Startup must stop so an operator can inspect and repair it.
var ErrIncompleteTail = errors.New("last record is not newline terminated")

var errUnterminatedRecord = ErrIncompleteTail

type appendRequest struct {
	ctx   context.Context
	event events.Event
	done  chan error
}

// Options bounds each segment. A single record may exceed this limit, up to
// MaxRecordBytes, and is written as the only record in its segment.
type Options struct {
	MaxSegmentBytes int64
}

// Record is one durable event. Error is intentionally absent: upstream and
// parser error strings can contain request data or secret material.
type Record struct {
	V            int                      `json:"v"`
	LogID        string                   `json:"log_id"`
	Seq          uint64                   `json:"seq"`
	Time         time.Time                `json:"time"`
	Type         events.Type              `json:"type"`
	Actor        string                   `json:"actor,omitempty"`
	Host         string                   `json:"host,omitempty"`
	Code         string                   `json:"code,omitempty"`
	Reason       string                   `json:"reason,omitempty"`
	RemoteAddr   string                   `json:"remote_addr,omitempty"`
	Count        int                      `json:"count,omitempty"`
	ConfigSHA256 string                   `json:"config_sha256,omitempty"`
	Context      *policycontext.Context   `json:"context,omitempty"`
	Deployment   *events.DeploymentSource `json:"deployment,omitempty"`
	Prev         string                   `json:"prev"`
	Hash         string                   `json:"hash"`
}

// Verification describes local chain consistency. CheckpointVerified reports
// signature and chain-position validation; TamperEvident remains false because
// this process cannot prove that the checkpoint and pinned key were retained
// outside the host's control.
type Verification struct {
	LogID                string `json:"log_id,omitempty"`
	Records              uint64 `json:"records"`
	LastHash             string `json:"last_hash,omitempty"`
	CheckpointSeq        uint64 `json:"checkpoint_seq,omitempty"`
	CheckpointKeyID      string `json:"checkpoint_key_id,omitempty"`
	CheckpointVerified   bool   `json:"checkpoint_verified"`
	InternallyConsistent bool   `json:"internally_consistent"`
	TamperEvident        bool   `json:"tamper_evident"`
	Note                 string `json:"note"`
}

// RepairPlan describes a repairable incomplete tail without changing it.
// Complete records with a valid hash are preserved by restoring their final
// newline; incomplete JSON bytes are discarded back to the last valid line.
type RepairPlan struct {
	Needed          bool
	Segment         uint64
	Offset          int64
	DiscardedBytes  int64
	TailSHA256      string
	RestoresRecord  bool
	VerifiedRecords uint64
}

// RepairResult reports the applied tail repair and its durable audit marker.
type RepairResult struct {
	Plan            RepairPlan
	Applied         bool
	RepairRecordSeq uint64
}

type recordPayload struct {
	V            int                      `json:"v"`
	LogID        string                   `json:"log_id"`
	Seq          uint64                   `json:"seq"`
	Time         time.Time                `json:"time"`
	Type         events.Type              `json:"type"`
	Actor        string                   `json:"actor,omitempty"`
	Host         string                   `json:"host,omitempty"`
	Code         string                   `json:"code,omitempty"`
	Reason       string                   `json:"reason,omitempty"`
	RemoteAddr   string                   `json:"remote_addr,omitempty"`
	Count        int                      `json:"count,omitempty"`
	ConfigSHA256 string                   `json:"config_sha256,omitempty"`
	Context      *policycontext.Context   `json:"context,omitempty"`
	Deployment   *events.DeploymentSource `json:"deployment,omitempty"`
	Prev         string                   `json:"prev"`
}

// Writer serializes appends within one process, takes an exclusive directory
// lock across processes, and fsyncs each complete record before advancing its
// sequence.
type Writer struct {
	mu              sync.Mutex
	appendMu        sync.Mutex
	dir             string
	maxSegmentBytes int64
	file            *os.File
	lockFile        *os.File
	segment         uint64
	segmentSize     int64
	seq             uint64
	logID           string
	prev            string
	failed          error
	closed          bool
	appendClosed    bool
	appendQueue     chan appendRequest
	appendWorkerEnd chan struct{}
	batchWindow     time.Duration
	syncFunc        func(*os.File) error
}

// Open creates a private log directory when needed, validates all existing
// segments, and refuses to reset a damaged chain.
func Open(ctx context.Context, dir string, options Options) (*Writer, error) {
	if err := secureFilesystemSupported(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dir) {
		return nil, errors.New("audit directory must be an absolute path")
	}
	if options.MaxSegmentBytes <= 0 {
		options.MaxSegmentBytes = DefaultSegmentSize
	}
	if options.MaxSegmentBytes > 1<<40 {
		return nil, errors.New("audit segment size is too large")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create audit directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect audit directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("audit path must be a real directory, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("audit directory permissions are broader than 0700")
	}
	lockFile, err := acquireDirectoryLock(filepath.Join(dir, "audit.lock"))
	if err != nil {
		return nil, fmt.Errorf("lock audit directory: %w", err)
	}
	retainLock := false
	defer func() {
		if !retainLock {
			_ = releaseDirectoryLock(lockFile)
		}
	}()
	if err := syncDirectory(filepath.Dir(dir)); err != nil {
		return nil, fmt.Errorf("sync audit parent directory: %w", err)
	}

	state, err := scanDirectory(ctx, dir, nil)
	if err != nil {
		return nil, err
	}
	segment := state.segment
	if segment == 0 {
		segment = 1
	}
	writer := &Writer{
		dir:             dir,
		lockFile:        lockFile,
		maxSegmentBytes: options.MaxSegmentBytes,
		segment:         segment,
		segmentSize:     state.segmentSize,
		seq:             state.verification.Records,
		logID:           state.verification.LogID,
		prev:            state.verification.LastHash,
		appendQueue:     make(chan appendRequest, appendQueueSize),
		appendWorkerEnd: make(chan struct{}),
		batchWindow:     appendBatchWindow,
	}
	retainLock = true
	go writer.runAppender()
	return writer, nil
}

// Append waits until the event's complete batch is fsynced. Concurrent events
// share one sync, while each caller still receives a durable result before it
// can respond or forward the request.
func (w *Writer) Append(ctx context.Context, event events.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Type == "" {
		return errors.New("audit event type is required")
	}
	if event.Count < 0 {
		return errors.New("audit event count must not be negative")
	}
	request := appendRequest{ctx: ctx, event: event, done: make(chan error, 1)}
	w.appendMu.Lock()
	if w.appendClosed {
		w.appendMu.Unlock()
		return ErrClosed
	}
	select {
	case w.appendQueue <- request:
		w.appendMu.Unlock()
	default:
		w.appendMu.Unlock()
		return ErrAppendQueueFull
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) runAppender() {
	defer close(w.appendWorkerEnd)
	for {
		first, ok := <-w.appendQueue
		if !ok {
			return
		}
		batch := make([]appendRequest, 0, appendBatchMax)
		batch = append(batch, first)
		timer := time.NewTimer(w.batchWindow)
		queueClosed := false
	collect:
		for len(batch) < appendBatchMax {
			select {
			case request, open := <-w.appendQueue:
				if !open {
					queueClosed = true
					break collect
				}
				batch = append(batch, request)
			case <-timer.C:
				break collect
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		w.appendBatch(batch)
		if queueClosed {
			return
		}
	}
}

func (w *Writer) appendBatch(batch []appendRequest) {
	results := make([]error, len(batch))
	active := make([]int, 0, len(batch))
	for i, request := range batch {
		if err := request.ctx.Err(); err != nil {
			results[i] = err
			continue
		}
		active = append(active, i)
	}
	if len(active) == 0 {
		respondToBatch(batch, results)
		return
	}

	w.mu.Lock()
	if w.closed {
		for _, i := range active {
			results[i] = ErrClosed
		}
		w.mu.Unlock()
		respondToBatch(batch, results)
		return
	}
	if w.failed != nil {
		failure := fmt.Errorf("audit writer is unavailable: %w", w.failed)
		for _, i := range active {
			results[i] = failure
		}
		w.mu.Unlock()
		respondToBatch(batch, results)
		return
	}

	wrote := false
	var writeFailure error
	for _, i := range active {
		if writeFailure != nil {
			results[i] = fmt.Errorf("audit writer is unavailable: %w", writeFailure)
			continue
		}
		request := batch[i]
		if err := request.ctx.Err(); err != nil {
			results[i] = err
			continue
		}
		logID := w.logID
		if logID == "" {
			logID, writeFailure = newLogID()
			if writeFailure != nil {
				results[i] = fmt.Errorf("create audit log ID: %w", writeFailure)
				continue
			}
		}
		at := request.event.Time
		if at.IsZero() {
			at = time.Now()
		}
		prev := w.prev
		if prev == "" {
			prev = zeroHash
		}
		record := Record{
			V: Version, LogID: logID, Seq: w.seq + 1, Time: at.UTC(), Type: request.event.Type,
			Actor: request.event.Actor, Host: request.event.Host, Code: request.event.Code, Reason: request.event.Reason,
			RemoteAddr: request.event.RemoteAddr, Count: request.event.Count, ConfigSHA256: request.event.ConfigSHA256,
			Context: request.event.Context, Deployment: request.event.Deployment, Prev: prev,
		}
		line, digest, err := encodeRecord(record)
		if err != nil {
			results[i] = fmt.Errorf("encode audit record: %w", err)
			continue
		}
		if len(line) > MaxRecordBytes {
			results[i] = fmt.Errorf("audit record exceeds %d bytes", MaxRecordBytes)
			continue
		}
		if w.segmentSize > 0 && w.segmentSize+int64(len(line)) > w.maxSegmentBytes {
			if err := w.rotate(); err != nil {
				writeFailure = fmt.Errorf("rotate audit segment: %w", err)
			}
		}
		if writeFailure == nil {
			writeFailure = w.openSegment()
			if writeFailure != nil {
				writeFailure = fmt.Errorf("open audit segment: %w", writeFailure)
			}
		}
		if writeFailure == nil {
			writeFailure = writeFull(w.file, line)
			if writeFailure != nil {
				writeFailure = fmt.Errorf("append audit record: %w", writeFailure)
			}
		}
		if writeFailure != nil {
			w.failed = writeFailure
			results[i] = writeFailure
			continue
		}
		w.seq = record.Seq
		w.logID = logID
		w.prev = hex.EncodeToString(digest[:])
		w.segmentSize += int64(len(line))
		results[i] = nil
		wrote = true
	}
	if writeFailure != nil {
		w.failed = writeFailure
		// A batch is acknowledged only after its writes are durable. If a later
		// append in this batch fails, earlier writes may still be buffered, so
		// report failure for every record whose success has not been synced.
		for _, i := range active {
			if results[i] == nil {
				results[i] = fmt.Errorf("audit writer could not confirm durable batch: %w", writeFailure)
			}
		}
	}
	if wrote && writeFailure == nil {
		if err := w.syncCurrentFile(); err != nil {
			w.failed = err
			for _, i := range active {
				if results[i] == nil {
					results[i] = fmt.Errorf("sync audit record batch: %w", err)
				}
			}
		}
	}
	w.mu.Unlock()
	respondToBatch(batch, results)
}

func (w *Writer) syncCurrentFile() error {
	if w.syncFunc != nil {
		return w.syncFunc(w.file)
	}
	return w.file.Sync()
}

func respondToBatch(batch []appendRequest, results []error) {
	for i, request := range batch {
		request.done <- results[i]
		close(request.done)
	}
}

// Close syncs any open segment and is safe to call more than once.
func (w *Writer) Close() error {
	w.appendMu.Lock()
	if !w.appendClosed {
		w.appendClosed = true
		close(w.appendQueue)
	}
	w.appendMu.Unlock()
	<-w.appendWorkerEnd

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return w.failed
	}
	w.closed = true
	if w.file == nil {
		if err := releaseDirectoryLock(w.lockFile); err != nil {
			w.failed = errors.Join(w.failed, err)
		}
		w.lockFile = nil
		return w.failed
	}
	syncErr := w.syncCurrentFile()
	closeErr := w.file.Close()
	w.file = nil
	if syncErr != nil {
		w.failed = errors.Join(w.failed, syncErr)
	}
	if closeErr != nil {
		w.failed = errors.Join(w.failed, closeErr)
	}
	if lockErr := releaseDirectoryLock(w.lockFile); lockErr != nil {
		w.failed = errors.Join(w.failed, lockErr)
	}
	w.lockFile = nil
	return w.failed
}

// VerifyDir checks the local hash chain. Its result explicitly disclaims
// tamper evidence because no external signed checkpoint is supplied here.
func VerifyDir(ctx context.Context, dir string) (Verification, error) {
	return verifyDirectory(ctx, dir, nil)
}

// InspectRepair verifies the complete chain and reports whether only the
// final segment has a repairable, unterminated record. It never writes files.
func InspectRepair(ctx context.Context, dir string) (RepairPlan, error) {
	if err := validateAuditDirectory(ctx, dir); err != nil {
		return RepairPlan{}, err
	}
	lockFile, err := acquireDirectoryReadLock(filepath.Join(dir, "audit.lock"))
	if err != nil {
		return RepairPlan{}, fmt.Errorf("lock audit directory for repair inspection: %w", err)
	}
	defer func() { _ = releaseDirectoryLock(lockFile) }()
	state, err := scanDirectoryForRepair(ctx, dir)
	if err != nil {
		return RepairPlan{}, err
	}
	return state.repairPlan, nil
}

// RepairDir repairs only an incomplete tail in the final segment. It holds
// the exclusive directory lock while atomically replacing that segment with
// its valid prefix and a durable audit.repaired marker.
func RepairDir(ctx context.Context, dir string) (RepairResult, error) {
	var result RepairResult
	if err := validateAuditDirectory(ctx, dir); err != nil {
		return result, err
	}
	lockFile, err := acquireDirectoryLock(filepath.Join(dir, "audit.lock"))
	if err != nil {
		return result, fmt.Errorf("lock audit directory for repair: %w", err)
	}
	defer func() { _ = releaseDirectoryLock(lockFile) }()

	state, err := scanDirectoryForRepair(ctx, dir)
	if err != nil {
		return result, err
	}
	result.Plan = state.repairPlan
	if !result.Plan.Needed {
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	action := "partial_tail_truncated"
	if result.Plan.RestoresRecord {
		action = "record_terminator_restored"
	}
	logID := state.verification.LogID
	if logID == "" {
		logID, err = newLogID()
		if err != nil {
			return result, fmt.Errorf("create audit repair log ID: %w", err)
		}
	}
	prev := state.verification.LastHash
	if prev == "" {
		prev = zeroHash
	}
	reason := fmt.Sprintf("segment=%d offset=%d discarded_bytes=%d tail_sha256=%s",
		result.Plan.Segment, result.Plan.Offset, result.Plan.DiscardedBytes, result.Plan.TailSHA256)
	markerLine, _, err := encodeRecord(Record{
		V: Version, LogID: logID, Seq: state.verification.Records + 1, Time: time.Now().UTC(),
		Type: events.AuditRepaired, Actor: "local", Code: action, Reason: reason, Prev: prev,
	})
	if err != nil {
		return result, fmt.Errorf("encode durable audit repair marker: %w", err)
	}
	if len(markerLine) > MaxRecordBytes {
		return result, errors.New("audit repair marker exceeds the maximum record size")
	}
	result.RepairRecordSeq = state.verification.Records + 1
	result.Applied, err = replaceAuditTailWithMarker(dir, result.Plan, markerLine)
	if err != nil {
		return result, fmt.Errorf("replace final audit segment with repair marker: %w", err)
	}
	if _, err := scanDirectory(ctx, dir, nil); err != nil {
		return result, fmt.Errorf("verify repaired audit chain: %w", err)
	}
	return result, nil
}

func replaceAuditTailWithMarker(dir string, plan RepairPlan, markerLine []byte) (bool, error) {
	path := segmentPath(dir, plan.Segment)
	info, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("inspect final audit segment: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return false, errors.New("final audit segment is not a private regular file")
	}
	source, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open final audit segment: %w", err)
	}
	defer source.Close()
	temporary, err := os.CreateTemp(dir, ".audit-repair-*")
	if err != nil {
		return false, fmt.Errorf("create repaired audit segment: %w", err)
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return false, fmt.Errorf("set repaired segment permissions: %w", err)
	}
	if plan.Offset > 0 {
		copied, err := io.CopyN(temporary, source, plan.Offset)
		if err != nil {
			_ = temporary.Close()
			return false, fmt.Errorf("copy verified audit prefix: %w", err)
		}
		if copied != plan.Offset {
			_ = temporary.Close()
			return false, io.ErrUnexpectedEOF
		}
	}
	if plan.RestoresRecord {
		if err := writeFull(temporary, []byte{'\n'}); err != nil {
			_ = temporary.Close()
			return false, fmt.Errorf("restore final record terminator: %w", err)
		}
	}
	if err := writeFull(temporary, markerLine); err != nil {
		_ = temporary.Close()
		return false, fmt.Errorf("write durable audit repair marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return false, fmt.Errorf("sync repaired audit segment: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close repaired audit segment: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return false, fmt.Errorf("publish repaired audit segment: %w", err)
	}
	published = true
	if err := syncDirectory(dir); err != nil {
		return true, fmt.Errorf("sync audit directory after repair: %w", err)
	}
	return true, nil
}

func validateAuditDirectory(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := secureFilesystemSupported(); err != nil {
		return err
	}
	if !filepath.IsAbs(dir) {
		return errors.New("audit directory must be an absolute path")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect audit directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("audit path must be a real directory, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("audit directory permissions are broader than 0700")
	}
	return nil
}

// ReadVerified visits records only while holding the audit directory's shared
// read lock and while verifying the complete local hash chain. Callers must
// discard any accumulated output when this returns an error; a later damaged
// record may be found after earlier records were visited.
func ReadVerified(ctx context.Context, dir string, visit func(Record) error) (Verification, error) {
	return readVerified(ctx, dir, nil, visit)
}

func verifyDirectory(ctx context.Context, dir string, checkpoint *Checkpoint) (Verification, error) {
	return readVerified(ctx, dir, checkpoint, nil)
}

func readVerified(ctx context.Context, dir string, checkpoint *Checkpoint, visit func(Record) error) (Verification, error) {
	if err := ctx.Err(); err != nil {
		return Verification{}, err
	}
	if err := secureFilesystemSupported(); err != nil {
		return Verification{}, err
	}
	if !filepath.IsAbs(dir) {
		return Verification{}, errors.New("audit directory must be an absolute path")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return Verification{}, fmt.Errorf("inspect audit directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Verification{}, errors.New("audit path must be a real directory, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Verification{}, errors.New("audit directory permissions are broader than 0700")
	}
	lockFile, err := acquireDirectoryReadLock(filepath.Join(dir, "audit.lock"))
	if err != nil {
		return Verification{}, fmt.Errorf("lock audit directory for verification: %w", err)
	}
	defer func() { _ = releaseDirectoryLock(lockFile) }()
	state, err := scanDirectoryWithVisitor(ctx, dir, checkpoint, visit)
	if err != nil {
		return Verification{}, err
	}
	state.verification.InternallyConsistent = true
	if checkpoint == nil {
		state.verification.TamperEvident = false
		state.verification.Note = "internal chain only; no external signed checkpoint was verified"
	} else {
		state.verification.TamperEvident = false
		state.verification.CheckpointVerified = true
		state.verification.CheckpointSeq = checkpoint.Seq
		state.verification.CheckpointKeyID = checkpoint.KeyID
		state.verification.Note = "chain matches the signed checkpoint and pinned public key supplied by the caller; external storage is not verified, so tamper evidence is not claimed"
	}
	return state.verification, nil
}

func secureFilesystemSupported() error {
	if runtime.GOOS == "windows" {
		return ErrUnsupportedPlatform
	}
	return nil
}

func (w *Writer) rotate() error {
	if w.file != nil {
		if err := w.syncCurrentFile(); err != nil {
			return err
		}
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}
	w.segment++
	w.segmentSize = 0
	return nil
}

func (w *Writer) openSegment() error {
	if w.file != nil {
		return nil
	}
	path := segmentPath(w.dir, w.segment)
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("audit segment must be a regular file, not a symlink")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return errors.New("audit segment permissions are broader than 0600")
		}
		w.file, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		return err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	w.file, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := syncDirectory(w.dir); err != nil {
		_ = w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

func encodeRecord(record Record) ([]byte, [sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	payload, err := marshalPayload(record)
	if err != nil {
		return nil, zero, err
	}
	digest, err := recordHash(record.Seq, record.Prev, payload)
	if err != nil {
		return nil, zero, err
	}
	line := make([]byte, 0, len(payload)+len(`,"hash":"`)+sha256.Size*2+3)
	line = append(line, payload[:len(payload)-1]...)
	line = append(line, `,"hash":"`...)
	line = append(line, hex.EncodeToString(digest[:])...)
	line = append(line, '"', '}', '\n')
	return line, digest, nil
}

func marshalPayload(record Record) ([]byte, error) {
	if record.V != Version || record.Seq == 0 || record.Type == "" {
		return nil, errors.New("invalid audit record version, sequence or type")
	}
	if !validHex(record.LogID, 16) || !validHex(record.Prev, sha256.Size) {
		return nil, errors.New("invalid audit log ID or previous hash")
	}
	if record.Time.IsZero() || record.Time.Location() != time.UTC {
		return nil, errors.New("audit record time must be non-zero UTC")
	}
	if record.Count < 0 {
		return nil, errors.New("audit record count must not be negative")
	}
	return json.Marshal(recordPayload{
		V: record.V, LogID: record.LogID, Seq: record.Seq, Time: record.Time,
		Type: record.Type, Actor: record.Actor, Host: record.Host, Code: record.Code,
		Reason: record.Reason, RemoteAddr: record.RemoteAddr, Count: record.Count,
		ConfigSHA256: record.ConfigSHA256, Context: record.Context, Deployment: record.Deployment, Prev: record.Prev,
	})
}

func recordHash(seq uint64, prevHex string, payload []byte) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	prev, err := hex.DecodeString(prevHex)
	if err != nil || len(prev) != sha256.Size || strings.ToLower(prevHex) != prevHex {
		return zero, errors.New("previous hash must be lowercase SHA-256 hex")
	}
	var sequence [8]byte
	binary.BigEndian.PutUint64(sequence[:], seq)
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(payload)))
	h := sha256.New()
	_, _ = h.Write([]byte(hashDomain))
	_, _ = h.Write(sequence[:])
	_, _ = h.Write(prev)
	_, _ = h.Write(length[:])
	_, _ = h.Write(payload)
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

func verifyRecord(line []byte, expectedSeq uint64, expectedPrev, expectedLogID string) (Record, [sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	var record Record
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Record{}, zero, fmt.Errorf("decode record: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Record{}, zero, errors.New("record contains trailing JSON")
	}
	if record.Seq != expectedSeq || record.Prev != expectedPrev {
		return Record{}, zero, errors.New("sequence or previous-hash discontinuity")
	}
	if expectedLogID != "" && record.LogID != expectedLogID {
		return Record{}, zero, errors.New("log ID changed within the chain")
	}
	if !validHex(record.Hash, sha256.Size) || strings.ToLower(record.Hash) != record.Hash {
		return Record{}, zero, errors.New("record hash must be lowercase SHA-256 hex")
	}
	marker := bytes.LastIndex(line, []byte(`,"hash":"`))
	if marker < 0 || !bytes.HasSuffix(line, []byte(`"}`)) {
		return Record{}, zero, errors.New("record hash must be the final JSON field")
	}
	payload := append(bytes.Clone(line[:marker]), '}')
	digest, err := recordHash(record.Seq, record.Prev, payload)
	if err != nil {
		return Record{}, zero, err
	}
	if record.Hash != hex.EncodeToString(digest[:]) {
		return Record{}, zero, errors.New("record hash mismatch")
	}
	return record, digest, nil
}

func validHex(value string, size int) bool {
	if len(value) != size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size
}

func newLogID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

type scanState struct {
	verification Verification
	segment      uint64
	segmentSize  int64
	repairPlan   RepairPlan
}

func scanDirectory(ctx context.Context, dir string, checkpoint *Checkpoint) (scanState, error) {
	return scanDirectoryWithVisitor(ctx, dir, checkpoint, nil)
}

func scanDirectoryWithVisitor(ctx context.Context, dir string, checkpoint *Checkpoint, visit func(Record) error) (scanState, error) {
	return scanDirectoryWithVisitorMode(ctx, dir, checkpoint, visit, false)
}

func scanDirectoryForRepair(ctx context.Context, dir string) (scanState, error) {
	return scanDirectoryWithVisitorMode(ctx, dir, nil, nil, true)
}

func scanDirectoryWithVisitorMode(ctx context.Context, dir string, checkpoint *Checkpoint, visit func(Record) error, allowFinalTail bool) (scanState, error) {
	var state scanState
	if err := ctx.Err(); err != nil {
		return state, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return state, fmt.Errorf("inspect audit directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return state, errors.New("audit path must be a real directory, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return state, errors.New("audit directory permissions are broader than 0700")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return state, fmt.Errorf("read audit directory: %w", err)
	}
	segments := make([]uint64, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, segmentPrefix) || !strings.HasSuffix(name, segmentSuffix) {
			continue
		}
		number := strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), segmentSuffix)
		segment, parseErr := strconv.ParseUint(number, 10, 64)
		if parseErr != nil || segment == 0 || segmentName(segment) != name {
			return state, fmt.Errorf("invalid audit segment name %q", name)
		}
		segments = append(segments, segment)
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i] < segments[j] })
	for i, segment := range segments {
		if segment != uint64(i+1) {
			return state, errors.New("audit segment sequence has a gap")
		}
	}
	state.segment = uint64(len(segments))
	if len(segments) == 0 {
		if checkpoint != nil {
			return state, errors.New("checkpoint refers to a record in an empty audit chain")
		}
		state.verification = Verification{}
		return state, nil
	}

	prev, logID, seq := zeroHash, "", uint64(0)
	checkpointFound := false
	for i, segment := range segments {
		if err := ctx.Err(); err != nil {
			return scanState{}, err
		}
		path := segmentPath(dir, segment)
		fileInfo, err := os.Lstat(path)
		if err != nil {
			return scanState{}, fmt.Errorf("inspect audit segment %s: %w", filepath.Base(path), err)
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 || !fileInfo.Mode().IsRegular() {
			return scanState{}, fmt.Errorf("audit segment %s is not a regular file", filepath.Base(path))
		}
		if fileInfo.Mode().Perm()&0o077 != 0 {
			return scanState{}, fmt.Errorf("audit segment %s permissions are broader than 0600", filepath.Base(path))
		}
		f, err := os.Open(path)
		if err != nil {
			return scanState{}, fmt.Errorf("open audit segment: %w", err)
		}
		lineReader := bufio.NewReaderSize(f, 64*1024)
		segmentRecords := 0
		lineOffset := int64(0)
		for {
			if err := ctx.Err(); err != nil {
				_ = f.Close()
				return scanState{}, err
			}
			line, eof, lineErr := readLine(lineReader)
			if lineErr != nil {
				if allowFinalTail && i == len(segments)-1 && errors.Is(lineErr, errUnterminatedRecord) {
					tailDigest := sha256.Sum256(line)
					plan := RepairPlan{
						Needed:          true,
						Segment:         segment,
						Offset:          lineOffset,
						TailSHA256:      hex.EncodeToString(tailDigest[:]),
						VerifiedRecords: seq,
					}
					record, digest, verifyErr := verifyRecord(line, seq+1, prev, logID)
					if verifyErr == nil {
						plan.Offset = fileInfo.Size()
						plan.RestoresRecord = true
						plan.VerifiedRecords++
						seq, prev, logID = record.Seq, hex.EncodeToString(digest[:]), record.LogID
						segmentRecords++
					} else {
						var completeJSON json.RawMessage
						if json.Unmarshal(line, &completeJSON) == nil {
							_ = f.Close()
							return scanState{}, errors.New("final unterminated audit record is complete JSON but does not verify; refusing automatic truncation")
						}
						plan.DiscardedBytes = int64(len(line))
					}
					state.repairPlan = plan
					break
				}
				_ = f.Close()
				return scanState{}, fmt.Errorf("read audit segment %s: %w", filepath.Base(path), lineErr)
			}
			if eof {
				break
			}
			record, digest, verifyErr := verifyRecord(line, seq+1, prev, logID)
			if verifyErr != nil {
				_ = f.Close()
				return scanState{}, fmt.Errorf("verify audit segment %s record %d: %w", filepath.Base(path), seq+1, verifyErr)
			}
			if visit != nil {
				if visitErr := visit(record); visitErr != nil {
					_ = f.Close()
					return scanState{}, fmt.Errorf("visit audit segment %s record %d: %w", filepath.Base(path), seq+1, visitErr)
				}
			}
			if checkpoint != nil && record.Seq == checkpoint.Seq {
				if record.LogID != checkpoint.LogID || record.Hash != checkpoint.Hash {
					_ = f.Close()
					return scanState{}, errors.New("checkpoint log ID or hash does not match the local chain")
				}
				checkpointFound = true
			}
			seq, prev, logID = record.Seq, hex.EncodeToString(digest[:]), record.LogID
			segmentRecords++
			lineOffset += int64(len(line) + 1)
		}
		if err := f.Close(); err != nil {
			return scanState{}, fmt.Errorf("close audit segment: %w", err)
		}
		if segmentRecords == 0 && i != len(segments)-1 {
			return scanState{}, errors.New("empty audit segment appears before a later segment")
		}
		if i == len(segments)-1 {
			state.segmentSize = fileInfo.Size()
		}
	}
	if checkpoint != nil && !checkpointFound {
		if checkpoint.Seq > seq {
			return scanState{}, errors.New("checkpoint is newer than the local audit tail; the log may have been truncated")
		}
		return scanState{}, errors.New("checkpoint position is missing from the local audit chain")
	}
	state.verification = Verification{LogID: logID, Records: seq, LastHash: prev}
	return state, nil
}

func readLine(reader *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > MaxRecordBytes+1 {
			return nil, false, fmt.Errorf("audit record exceeds %d bytes", MaxRecordBytes)
		}
		line = append(line, fragment...)
		switch err {
		case nil:
			return line[:len(line)-1], false, nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(line) == 0 {
				return nil, true, nil
			}
			return line, false, errUnterminatedRecord
		default:
			return nil, false, err
		}
	}
}

func writeFull(file *os.File, data []byte) error {
	for len(data) > 0 {
		n, err := file.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func segmentName(segment uint64) string             { return fmt.Sprintf("audit-%06d.jsonl", segment) }
func segmentPath(dir string, segment uint64) string { return filepath.Join(dir, segmentName(segment)) }

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
