package audit

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/events"
)

func TestSignedCheckpointVerifiesPinnedChainPositionAndTail(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyID, err := KeyIDForPublicKey(publicKey)
	if err != nil {
		t.Fatalf("KeyIDForPublicKey: %v", err)
	}
	writer, err := Open(ctx, dir, Options{MaxSegmentBytes: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := range 2 {
		if err := writer.Append(ctx, events.Event{Type: events.AccessAllowed, Host: "app.localhost", Code: "allowed", Time: time.Date(2026, 10, 25, 12, i, 0, 0, time.UTC)}); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	checkpoint, err := CreateCheckpoint(ctx, dir, privateKey, keyID, time.Date(2026, 10, 25, 12, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}
	if checkpoint.Seq != 2 || checkpoint.LogID == "" || checkpoint.Hash == "" {
		t.Fatalf("checkpoint = %+v", checkpoint)
	}
	data, err := MarshalCheckpoint(checkpoint)
	if err != nil {
		t.Fatalf("MarshalCheckpoint: %v", err)
	}
	decoded, err := DecodeCheckpoint(data)
	if err != nil {
		t.Fatalf("DecodeCheckpoint: %v", err)
	}
	verified, err := VerifyDirWithCheckpoint(ctx, dir, decoded, publicKey)
	if err != nil {
		t.Fatalf("VerifyDirWithCheckpoint: %v", err)
	}
	if verified.TamperEvident || !verified.CheckpointVerified || !verified.InternallyConsistent || verified.CheckpointSeq != 2 || verified.CheckpointKeyID != keyID {
		t.Fatalf("verification = %+v", verified)
	}

	writer, err = Open(ctx, dir, Options{MaxSegmentBytes: 1})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := writer.Append(ctx, events.Event{Type: events.AccessDenied, Host: "app.localhost", Code: "denied", Time: time.Date(2026, 10, 25, 12, 3, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("append tail: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tail: %v", err)
	}
	verified, err = VerifyDirWithCheckpoint(ctx, dir, checkpoint, publicKey)
	if err != nil || verified.Records != 3 || !verified.CheckpointVerified || verified.TamperEvident {
		t.Fatalf("verify tail with older checkpoint = %+v, %v", verified, err)
	}
}

func TestCheckpointRejectsWrongKeyTamperAndTruncation(t *testing.T) {
	requireAuditFS(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "audit")
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyID, _ := KeyIDForPublicKey(publicKey)
	writer, err := Open(ctx, dir, Options{MaxSegmentBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := writer.Append(ctx, events.Event{Type: events.AccessAllowed, Host: "app.localhost", Code: "allowed", Time: time.Date(2026, 10, 25, 12, i, 0, 0, time.UTC)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := CreateCheckpoint(ctx, dir, privateKey, keyID, time.Date(2026, 10, 25, 12, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDirWithCheckpoint(ctx, dir, checkpoint, wrongPublicKey); err == nil {
		t.Fatal("checkpoint verified with an unpinned key")
	}
	changed := checkpoint
	changed.Hash = zeroHash
	if _, err := VerifyDirWithCheckpoint(ctx, dir, changed, publicKey); err == nil {
		t.Fatal("changed checkpoint field verified")
	}
	path := segmentPath(dir, 1)
	line, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDirWithCheckpoint(ctx, dir, checkpoint, publicKey); err == nil {
		t.Fatal("checkpoint verified after its chain record was modified")
	}
	if err := os.WriteFile(path, line, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(segmentPath(dir, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDirWithCheckpoint(ctx, dir, checkpoint, publicKey); err == nil {
		t.Fatal("truncated segment unexpectedly verified")
	}
}

func TestCheckpointRejectsMalformedInputAndEmptyChain(t *testing.T) {
	requireAuditFS(t)
	if _, err := DecodeCheckpoint([]byte(`{} {}`)); err == nil {
		t.Fatal("DecodeCheckpoint accepted trailing JSON")
	}
	if _, err := DecodeCheckpoint([]byte(`{"v":1,"unknown":true}`)); err == nil {
		t.Fatal("DecodeCheckpoint accepted an unknown field")
	}
	if _, err := DecodeCheckpoint([]byte(`{"v":1,"v":1}`)); err == nil {
		t.Fatal("DecodeCheckpoint accepted a duplicate field")
	}
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	privateKey := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	if _, err := CreateCheckpoint(context.Background(), dir, privateKey, "key-1", time.Now().UTC()); err == nil {
		t.Fatal("CreateCheckpoint accepted an empty chain")
	}
}
