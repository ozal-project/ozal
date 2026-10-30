package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/policycontext"
)

func TestAppendWritesSafeRecordAndReopensChain(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	observedAt := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	decisionContext := policycontext.Build(policycontext.Input{
		ObservedAt: observedAt,
		Route:      policycontext.Known("app.example.test", policycontext.SourceRouteTable),
	})
	if err := writer.Append(ctx, events.Event{
		Time: observedAt, Type: events.AccessDenied, Actor: "user-17", Host: "app.example.test",
		Code: "email_not_allowed", Reason: "email is not on the allowlist", Error: "secret-marker",
		RemoteAddr: "203.0.113.7:4210", Context: &decisionContext,
		Deployment: &events.DeploymentSource{Project: "portfolio", RepositoryID: 123, Ref: "refs/heads/main", CommitSHA: strings.Repeat("a", 40)},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := segmentPath(dir, 1)
	line, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit line: %v", err)
	}
	if !bytes.HasSuffix(line, []byte("\n")) || bytes.Count(line, []byte("\n")) != 1 {
		t.Fatalf("record is not one newline-terminated JSON line: %q", line)
	}
	if strings.Contains(string(line), "secret-marker") {
		t.Fatalf("event error leaked into audit record: %s", line)
	}
	var record Record
	if err := json.Unmarshal(bytes.TrimSuffix(line, []byte("\n")), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if !bytes.Contains(line, []byte(`"project":"portfolio"`)) || !bytes.Contains(line, []byte(`"repository_id":123`)) {
		t.Fatalf("durable event omitted reviewed deployment provenance: %s", line)
	}
	if record.V != Version || record.Seq != 1 || record.Type != events.AccessDenied || record.Prev != zeroHash ||
		!record.Time.Equal(observedAt) || record.Context == nil || record.Hash == "" || record.LogID == "" {
		t.Fatalf("record = %+v", record)
	}
	if len(record.Hash) != sha256.Size*2 || strings.ToLower(record.Hash) != record.Hash {
		t.Fatalf("hash is not lowercase SHA-256 hex: %q", record.Hash)
	}
	if mode := fileMode(t, path); mode.Perm() != 0o600 {
		t.Fatalf("segment mode = %04o, want 0600", mode.Perm())
	}
	if mode := fileMode(t, dir); mode.Perm() != 0o700 {
		t.Fatalf("directory mode = %04o, want 0700", mode.Perm())
	}

	verified, err := VerifyDir(ctx, dir)
	if err != nil {
		t.Fatalf("VerifyDir: %v", err)
	}
	if !verified.InternallyConsistent || verified.TamperEvident || verified.Records != 1 || !strings.Contains(verified.Note, "no external signed checkpoint") {
		t.Fatalf("verification overstates its guarantee: %+v", verified)
	}

	reopened, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := reopened.Append(ctx, events.Event{Time: observedAt.Add(time.Second), Type: events.AccessAllowed, Host: "app.example.test"}); err != nil {
		t.Fatalf("append after reopen: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened writer: %v", err)
	}
	lines := readAuditLines(t, path)
	var first, second Record
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(lines[1], &second); err != nil {
		t.Fatal(err)
	}
	if first.Seq != 1 || second.Seq != 2 || second.Prev != first.Hash || first.LogID != second.LogID {
		t.Fatalf("reopened sequence broke chain: first=%+v second=%+v", first, second)
	}
}

func TestAdminMutationIntentIsStoredInVerifiedChain(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	event := events.Event{
		Type: events.AdminMutationRequested, Actor: "local", Host: "ana@example.com",
		Code: "user.invite", Reason: "authenticated operator requested mutation",
	}
	if err := writer.Append(ctx, event); err != nil {
		t.Fatalf("Append admin intent: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var got Record
	verification, err := ReadVerified(ctx, dir, func(record Record) error {
		got = record
		return nil
	})
	if err != nil || verification.Records != 1 {
		t.Fatalf("ReadVerified = %+v, %v", verification, err)
	}
	if got.Type != events.AdminMutationRequested || got.Actor != event.Actor || got.Host != event.Host || got.Code != event.Code || got.Reason != event.Reason {
		t.Fatalf("verified admin intent = %+v, want %+v", got, event)
	}
}

func TestReadVerifiedVisitsRecordsUnderTheChainCheck(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	contextSnapshot := policycontext.Build(policycontext.Input{
		ObservedAt:    at,
		SessionMethod: policycontext.Known("passkey", policycontext.SourceSession),
	})
	for i, kind := range []events.Type{events.AccessAllowed, events.AccessDenied} {
		if err := writer.Append(ctx, events.Event{Time: at.Add(time.Duration(i) * time.Second), Type: kind, Host: "app.example.test", Actor: "subject-1", Context: &contextSnapshot}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var records []Record
	verification, err := ReadVerified(ctx, dir, func(record Record) error {
		records = append(records, record)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadVerified: %v", err)
	}
	if len(records) != 2 || records[0].Seq != 1 || records[1].Seq != 2 || records[0].Context == nil || records[0].Actor != "subject-1" {
		t.Fatalf("visited records = %+v", records)
	}
	if verification.Records != 2 || !verification.InternallyConsistent || verification.TamperEvident {
		t.Fatalf("verification overstates replay evidence: %+v", verification)
	}

	path := segmentPath(dir, 1)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("access.denied"), []byte("access.den1ed"), 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	visited := 0
	if _, err := ReadVerified(ctx, dir, func(Record) error { visited++; return nil }); err == nil {
		t.Fatal("ReadVerified accepted a tampered chain")
	}
	if visited != 1 {
		t.Fatalf("visitor saw %d records before the later corrupt record; caller must discard partial work on error", visited)
	}
}

func TestRecordHashUsesDomainSequenceRawPreviousHashAndPayloadLength(t *testing.T) {
	record := Record{
		V: Version, LogID: "0123456789abcdef0123456789abcdef", Seq: 17,
		Time: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		Type: events.AccessAllowed, Host: "app.example.test", Prev: zeroHash,
	}
	line, _, err := encodeRecord(record)
	if err != nil {
		t.Fatalf("encodeRecord: %v", err)
	}
	marker := bytes.LastIndex(line, []byte(`,"hash":"`))
	if marker < 0 {
		t.Fatalf("record has no final hash field: %s", line)
	}
	withoutHash := append(bytes.Clone(line[:marker]), '}')
	var got Record
	if err := json.Unmarshal(bytes.TrimSuffix(line, []byte("\n")), &got); err != nil {
		t.Fatal(err)
	}
	prev, _ := hex.DecodeString(got.Prev)
	var sequence, length [8]byte
	binary.BigEndian.PutUint64(sequence[:], got.Seq)
	binary.BigEndian.PutUint64(length[:], uint64(len(withoutHash)))
	input := bytes.Join([][]byte{[]byte(hashDomain), sequence[:], prev, length[:], withoutHash}, nil)
	want := sha256.Sum256(input)
	if got.Hash != hex.EncodeToString(want[:]) {
		t.Fatalf("hash = %s, independently computed %s", got.Hash, hex.EncodeToString(want[:]))
	}
}

func TestRotationKeepsOneGlobalChain(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{MaxSegmentBytes: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := writer.Append(ctx, events.Event{Time: at.Add(time.Duration(i) * time.Second), Type: events.AccessAllowed, Code: "allowed"}); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for i := uint64(1); i <= 3; i++ {
		if _, err := os.Stat(segmentPath(dir, i)); err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
	}
	verified, err := VerifyDir(ctx, dir)
	if err != nil || verified.Records != 3 {
		t.Fatalf("VerifyDir = %+v, %v", verified, err)
	}
}

func TestConcurrentAppendsAreSerialized(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{MaxSegmentBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 4*16)
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for worker := 0; worker < 4; worker++ {
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errCh <- writer.Append(ctx, events.Event{Time: at, Type: events.AccessAllowed, Code: "allowed"})
			}()
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyDir(ctx, dir)
	if err != nil || verified.Records != 64 {
		t.Fatalf("VerifyDir = %+v, %v; want 64 records", verified, err)
	}
}

func TestConcurrentAppendsShareOneDurableSync(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	writer.batchWindow = 100 * time.Millisecond
	var syncCalls atomic.Int32
	writer.syncFunc = func(file *os.File) error {
		syncCalls.Add(1)
		return file.Sync()
	}

	const concurrentAppends = 32
	ready := make(chan struct{}, concurrentAppends)
	start := make(chan struct{})
	errCh := make(chan error, concurrentAppends)
	var workers sync.WaitGroup
	for range concurrentAppends {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ready <- struct{}{}
			<-start
			errCh <- writer.Append(ctx, events.Event{Type: events.AccessAllowed, Host: "app.example.test"})
		}()
	}
	for range concurrentAppends {
		<-ready
	}
	close(start)
	workers.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if calls := syncCalls.Load(); calls == 0 || calls >= concurrentAppends {
		t.Fatalf("fsync calls = %d for %d concurrent appends; expected at least two records per sync", calls, concurrentAppends)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyDir(ctx, dir)
	if err != nil || verified.Records != concurrentAppends {
		t.Fatalf("VerifyDir = %+v, %v; want %d records", verified, err, concurrentAppends)
	}
}

func TestFailedBatchDoesNotAcknowledgeEarlierUnsyncedRecords(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	writer.batchWindow = 100 * time.Millisecond
	syncFailure := errors.New("injected sync failure")
	writer.syncFunc = func(*os.File) error { return syncFailure }

	const concurrentAppends = 2
	start := make(chan struct{})
	errCh := make(chan error, concurrentAppends)
	var workers sync.WaitGroup
	for range concurrentAppends {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errCh <- writer.Append(ctx, events.Event{Type: events.AccessAllowed, Host: "app.example.test"})
		}()
	}
	close(start)
	workers.Wait()
	close(errCh)
	for err := range errCh {
		if !errors.Is(err, syncFailure) {
			t.Fatalf("Append error = %v, want injected sync failure", err)
		}
	}
	if err := writer.Close(); !errors.Is(err, syncFailure) {
		t.Fatalf("Close error = %v, want injected sync failure", err)
	}
}

func TestOpenAllowsOnlyOneWriterPerDirectory(t *testing.T) {
	requireAuditFS(t)
	dir := filepath.Join(t.TempDir(), "audit")
	first, err := Open(context.Background(), dir, Options{})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if second, err := Open(context.Background(), dir, Options{}); !errors.Is(err, ErrDirectoryLocked) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second Open error = %v, want %v", err, ErrDirectoryLocked)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	third, err := Open(context.Background(), dir, Options{})
	if err != nil {
		t.Fatalf("Open after close: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("third Close: %v", err)
	}
}

func TestProcessKillAfterAppendCanRestartAndVerify(t *testing.T) {
	requireAuditFS(t)
	dir := filepath.Join(t.TempDir(), "audit")
	readyPath := filepath.Join(filepath.Dir(dir), "writer-ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAuditProcessCrashHelper$")
	cmd.Env = append(os.Environ(), "OZAL_AUDIT_CRASH_TEST_DIR="+dir)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start audit writer process: %v", err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect child readiness: %v", err)
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			waitErr := cmd.Wait()
			finished = true
			t.Fatalf("child did not finish a durable append; wait err=%v, output=%q", waitErr, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill audit writer process: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("writer process exited cleanly; expected SIGKILL")
	}
	finished = true

	verified, err := VerifyDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("verify after process kill: %v", err)
	}
	if verified.Records != 1 || !verified.InternallyConsistent {
		t.Fatalf("verification after process kill = %+v", verified)
	}

	writer, err := Open(context.Background(), dir, Options{})
	if err != nil {
		t.Fatalf("restart writer after process kill: %v", err)
	}
	if err := writer.Append(context.Background(), events.Event{Type: events.AccessAllowed, Code: "after_restart"}); err != nil {
		t.Fatalf("append after restart: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close restarted writer: %v", err)
	}
	verified, err = VerifyDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("verify restarted log: %v", err)
	}
	if verified.Records != 2 || !verified.InternallyConsistent {
		t.Fatalf("verification after restart = %+v", verified)
	}
}

// TestAuditProcessCrashHelper is run in a child test process by
// TestProcessKillAfterAppendCanRestartAndVerify. It deliberately leaves the
// writer open so the parent can kill the process after Append has fsynced.
func TestAuditProcessCrashHelper(t *testing.T) {
	dir := os.Getenv("OZAL_AUDIT_CRASH_TEST_DIR")
	if dir == "" {
		return
	}
	writer, err := Open(context.Background(), dir, Options{})
	if err != nil {
		t.Fatalf("open child writer: %v", err)
	}
	if err := writer.Append(context.Background(), events.Event{Type: events.AccessDenied, Code: "before_kill"}); err != nil {
		t.Fatalf("child append: %v", err)
	}
	readyPath := filepath.Join(filepath.Dir(dir), "writer-ready")
	if err := os.WriteFile(readyPath, []byte("durable\n"), 0o600); err != nil {
		t.Fatalf("signal durable append: %v", err)
	}
	select {}
}

func TestVerifyDirRefusesConcurrentWriter(t *testing.T) {
	requireAuditFS(t)
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(context.Background(), dir, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := VerifyDir(context.Background(), dir); !errors.Is(err, ErrDirectoryLocked) {
		t.Fatalf("VerifyDir while writer is open error = %v, want %v", err, ErrDirectoryLocked)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := VerifyDir(context.Background(), dir); err != nil {
		t.Fatalf("VerifyDir after close: %v", err)
	}
}

func TestOpenRejectsSymlinkedOrBroadLockFile(t *testing.T) {
	requireAuditFS(t)
	root := t.TempDir()
	linkedDir := filepath.Join(root, "linked")
	if err := os.Mkdir(linkedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(linkedDir, "audit.lock")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Open(context.Background(), linkedDir, Options{}); err == nil {
		t.Fatal("Open accepted a symlinked lock file")
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "untouched" {
		t.Fatalf("lock symlink target changed: %q, %v", contents, err)
	}

	wideDir := filepath.Join(root, "wide")
	writer, err := Open(context.Background(), wideDir, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(wideDir, "audit.lock")
	if err := os.Chmod(lockPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), wideDir, Options{}); err == nil {
		t.Fatal("Open accepted a lock file accessible to group or other users")
	}
	if mode := fileMode(t, lockPath).Perm(); mode != 0o644 {
		t.Fatalf("broad lock permissions were silently changed to %04o", mode)
	}
}

func TestVerifyRejectsEditReorderingTruncationAndMissingSegment(t *testing.T) {
	for _, name := range []string{"edit", "reorder", "truncate", "missing segment"} {
		t.Run(name, func(t *testing.T) {
			requireAuditFS(t)
			ctx := context.Background()
			dir := filepath.Join(t.TempDir(), "audit")
			writer, err := Open(ctx, dir, Options{MaxSegmentBytes: 1})
			if err != nil {
				t.Fatal(err)
			}
			for i, reason := range []string{"first", "second"} {
				if err := writer.Append(ctx, events.Event{
					Time: time.Date(2026, 10, 25, 12, 0, i, 0, time.UTC), Type: events.AccessDenied, Reason: reason,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			firstPath, secondPath := segmentPath(dir, 1), segmentPath(dir, 2)
			first, err := os.ReadFile(firstPath)
			if err != nil {
				t.Fatal(err)
			}
			second, err := os.ReadFile(secondPath)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "edit":
				first = bytes.Replace(first, []byte(`"reason":"first"`), []byte(`"reason":"forged"`), 1)
				if err := os.WriteFile(firstPath, first, 0o600); err != nil {
					t.Fatal(err)
				}
			case "reorder":
				if err := os.WriteFile(firstPath, second, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(secondPath, first, 0o600); err != nil {
					t.Fatal(err)
				}
			case "truncate":
				if err := os.WriteFile(secondPath, bytes.TrimSuffix(second, []byte("\n")), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing segment":
				if err := os.Remove(firstPath); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := VerifyDir(ctx, dir); err == nil {
				t.Fatal("VerifyDir accepted a damaged chain")
			}
		})
	}
}

func TestOpenDoesNotResetDamagedChain(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(ctx, events.Event{Time: time.Now().UTC(), Type: events.AccessDenied}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := segmentPath(dir, 1)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Replace(before, []byte(`"type":"access.denied"`), []byte(`"type":"login.failed"`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt, _ := os.ReadFile(path)
	if _, err := Open(ctx, dir, Options{}); err == nil {
		t.Fatal("Open silently reset a damaged chain")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(corrupt, after) {
		t.Fatal("Open changed the damaged log while refusing it")
	}
}

func TestOpenRejectsBroadDirectoryPermissionsWithoutChangingThem(t *testing.T) {
	requireAuditFS(t)
	dir := filepath.Join(t.TempDir(), "broad")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), dir, Options{}); err == nil {
		t.Fatal("Open accepted a broadly readable directory")
	}
	if mode := fileMode(t, dir); mode.Perm() != 0o755 {
		t.Fatalf("Open changed the caller's directory mode to %04o", mode.Perm())
	}
}

func TestOpenRejectsBroadSegmentPermissionsWithoutChangingThem(t *testing.T) {
	requireAuditFS(t)
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(context.Background(), events.Event{Time: time.Now().UTC(), Type: events.AccessAllowed}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := segmentPath(dir, 1)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), dir, Options{}); err == nil {
		t.Fatal("Open accepted a broadly readable segment")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) || fileMode(t, path).Perm() != 0o644 {
		t.Fatal("Open changed a broadly readable segment while refusing it")
	}
}

func TestOpenRejectsSymlinkedAuditSegment(t *testing.T) {
	requireAuditFS(t)
	dir := filepath.Join(t.TempDir(), "audit")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	contents := []byte("must remain untouched\n")
	if err := os.WriteFile(outside, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, segmentPath(dir, 1)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Open(context.Background(), dir, Options{}); err == nil {
		t.Fatal("Open accepted a symlinked audit segment")
	}
	after, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, contents) {
		t.Fatalf("Open changed the symlink target: %q", after)
	}
}

func TestAppendHonorsCancellationAndClosedWriter(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := writer.Append(canceled, events.Event{Type: events.AccessAllowed}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Append with canceled context = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(ctx, events.Event{Type: events.AccessAllowed}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Append after Close = %v, want ErrClosed", err)
	}
}

func TestInterruptedRecordRequiresExplicitRepairBeforeAppend(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	w, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(ctx, events.Event{Type: events.AccessAllowed}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(segmentPath(dir, 1), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	partial := []byte(`{"v":1,"log_id":"interrupted`)
	if _, err := f.Write(partial); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if w, err := Open(ctx, dir, Options{}); !errors.Is(err, ErrIncompleteTail) {
		if w != nil {
			_ = w.Close()
		}
		t.Fatalf("Open after interruption: %v, want incomplete tail", err)
	}
	plan, err := InspectRepair(ctx, dir)
	if err != nil || !plan.Needed || plan.RestoresRecord || plan.DiscardedBytes != int64(len(partial)) || plan.VerifiedRecords != 1 {
		t.Fatalf("repair plan: %+v, %v", plan, err)
	}
	result, err := RepairDir(ctx, dir)
	if err != nil || !result.Applied || result.RepairRecordSeq != 2 {
		t.Fatalf("repair: %+v, %v", result, err)
	}
	w, err = Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(ctx, events.Event{Type: events.AccessDenied}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var kinds []events.Type
	v, err := ReadVerified(ctx, dir, func(r Record) error { kinds = append(kinds, r.Type); return nil })
	if err != nil || v.Records != 3 || len(kinds) != 3 || kinds[0] != events.AccessAllowed || kinds[1] != events.AuditRepaired || kinds[2] != events.AccessDenied {
		t.Fatalf("recovered records: %+v, %v, %v", v, kinds, err)
	}
}

func TestRepairPreservesACompleteRecordMissingOnlyItsNewline(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	firstTime := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	if err := writer.Append(ctx, events.Event{Time: firstTime, Type: events.AccessAllowed}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var first Record
	if _, err := ReadVerified(ctx, dir, func(record Record) error {
		first = record
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	completeRecord, _, err := encodeRecord(Record{
		V: Version, LogID: first.LogID, Seq: 2, Time: firstTime.Add(time.Second),
		Type: events.AccessDenied, Prev: first.Hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	completeRecord = bytes.TrimSuffix(completeRecord, []byte("\n"))
	file, err := os.OpenFile(segmentPath(dir, 1), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(completeRecord); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	plan, err := InspectRepair(ctx, dir)
	if err != nil || !plan.Needed || !plan.RestoresRecord || plan.DiscardedBytes != 0 || plan.VerifiedRecords != 2 {
		t.Fatalf("InspectRepair = %+v, %v", plan, err)
	}
	result, err := RepairDir(ctx, dir)
	if err != nil || !result.Applied || result.RepairRecordSeq != 3 {
		t.Fatalf("RepairDir = %+v, %v", result, err)
	}
	var records []Record
	verification, err := ReadVerified(ctx, dir, func(record Record) error {
		records = append(records, record)
		return nil
	})
	if err != nil || verification.Records != 3 || len(records) != 3 {
		t.Fatalf("ReadVerified = %+v, records=%+v, err=%v", verification, records, err)
	}
	if records[1].Type != events.AccessDenied || records[2].Type != events.AuditRepaired || records[2].Code != "record_terminator_restored" {
		t.Fatalf("restored and repair records = %+v", records[1:])
	}
}

func TestRepairRefusesCompleteJSONTailWithInvalidHash(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(ctx, events.Event{Type: events.AccessAllowed}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := segmentPath(dir, 1)
	line, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line = bytes.TrimSuffix(line, []byte("\n"))
	marker := bytes.LastIndex(line, []byte(`"hash":"`)) + len(`"hash":"`)
	if line[marker] == '0' {
		line[marker] = '1'
	} else {
		line[marker] = '0'
	}
	if err := os.WriteFile(path, line, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectRepair(ctx, dir); err == nil || !strings.Contains(err.Error(), "complete JSON but does not verify") {
		t.Fatalf("InspectRepair = %v, want refusal of complete invalid JSON", err)
	}
	if _, err := Open(ctx, dir, Options{}); err == nil {
		t.Fatal("Open accepted the invalid chain after inspection")
	}
}

func TestRepairRefusesToRunWhileWriterIsActive(t *testing.T) {
	requireAuditFS(t)
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RepairDir(context.Background(), dir); !errors.Is(err, ErrDirectoryLocked) {
		t.Fatalf("RepairDir error = %v, want %v", err, ErrDirectoryLocked)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func readAuditLines(t *testing.T, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(parts) == 1 && len(parts[0]) == 0 {
		return nil
	}
	return parts
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}

func FuzzVerifyRecordDoesNotPanic(f *testing.F) {
	seed, _, err := encodeRecord(Record{
		V: Version, LogID: "0123456789abcdef0123456789abcdef", Seq: 1,
		Time: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), Type: events.AccessAllowed, Prev: zeroHash,
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(bytes.TrimSuffix(seed, []byte("\n")))
	f.Add([]byte(`{"seq":1,"hash":"`))
	f.Fuzz(func(t *testing.T, line []byte) {
		if len(line) > MaxRecordBytes {
			return
		}
		_, _, _ = verifyRecord(line, 1, zeroHash, "")
	})
}

func TestWindowsAuditStorageFailsClosedBeforeCreatingFiles(t *testing.T) {
	if runtime.GOOS != "windows" {
		return
	}
	dir := filepath.Join(t.TempDir(), "audit")
	if _, err := Open(context.Background(), dir, Options{}); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Open = %v, want ErrUnsupportedPlatform", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open created audit storage on unsupported ACL platform: %v", err)
	}
}

func requireAuditFS(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("private ACL support is not implemented on Windows")
	}
}
