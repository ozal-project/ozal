package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	checkpointVersion = 1
	checkpointDomain  = "ozal-audit-checkpoint-v1\x00"
	maxKeyIDLength    = 64
)

// Checkpoint signs a particular chain position. Keeping a copy of this record
// and its pinned public key outside the gateway host is required for the
// signature to provide evidence against a host-level rewrite.
type Checkpoint struct {
	V         int       `json:"v"`
	LogID     string    `json:"log_id"`
	Seq       uint64    `json:"seq"`
	Hash      string    `json:"hash"`
	Time      time.Time `json:"time"`
	KeyID     string    `json:"key_id"`
	Signature string    `json:"signature"`
}

type checkpointPayload struct {
	V     int       `json:"v"`
	LogID string    `json:"log_id"`
	Seq   uint64    `json:"seq"`
	Hash  string    `json:"hash"`
	Time  time.Time `json:"time"`
	KeyID string    `json:"key_id"`
}

// CreateCheckpoint signs the current chain tail. The audit directory must be
// quiescent; VerifyDir obtains a shared lock and refuses to race an active
// writer. The returned checkpoint is not automatically copied off-host.
func CreateCheckpoint(ctx context.Context, dir string, privateKey ed25519.PrivateKey, keyID string, at time.Time) (Checkpoint, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Checkpoint{}, errors.New("audit checkpoint requires a 64-byte Ed25519 private key")
	}
	if !validKeyID(keyID) {
		return Checkpoint{}, errors.New("audit checkpoint key ID must be 1-64 ASCII letters, digits, dot, underscore or hyphen")
	}
	if at.IsZero() {
		return Checkpoint{}, errors.New("audit checkpoint time is required")
	}
	verified, err := VerifyDir(ctx, dir)
	if err != nil {
		return Checkpoint{}, err
	}
	if verified.Records == 0 || verified.LogID == "" || verified.LastHash == "" {
		return Checkpoint{}, errors.New("cannot checkpoint an empty audit chain")
	}
	checkpoint := Checkpoint{
		V: checkpointVersion, LogID: verified.LogID, Seq: verified.Records,
		Hash: verified.LastHash, Time: at.UTC(), KeyID: keyID,
	}
	payload, err := marshalCheckpointPayload(checkpoint)
	if err != nil {
		return Checkpoint{}, err
	}
	message := make([]byte, 0, len(checkpointDomain)+len(payload))
	message = append(message, checkpointDomain...)
	message = append(message, payload...)
	checkpoint.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message))
	return checkpoint, nil
}

// VerifyDirWithCheckpoint checks the Ed25519 signature against the caller's
// pinned public key and verifies that the full local chain contains the signed
// position and has a consistent tail after it. The result does not assert that
// the checkpoint or pinned key was stored outside the gateway host.
func VerifyDirWithCheckpoint(ctx context.Context, dir string, checkpoint Checkpoint, publicKey ed25519.PublicKey) (Verification, error) {
	if err := verifyCheckpointSignature(checkpoint, publicKey); err != nil {
		return Verification{}, err
	}
	return verifyDirectory(ctx, dir, &checkpoint)
}

// DecodeCheckpoint parses a bounded checkpoint file and rejects unknown or
// trailing JSON. Signature and semantic validation occur during verification.
func DecodeCheckpoint(data []byte) (Checkpoint, error) {
	if len(data) == 0 || len(data) > 64<<10 {
		return Checkpoint{}, errors.New("audit checkpoint must be between 1 byte and 64 KiB")
	}
	if err := rejectDuplicateCheckpointFields(data); err != nil {
		return Checkpoint{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var checkpoint Checkpoint
	if err := decoder.Decode(&checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("decode audit checkpoint: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Checkpoint{}, errors.New("audit checkpoint contains trailing JSON")
	}
	return checkpoint, nil
}

// MarshalCheckpoint emits the checkpoint as one compact JSON object followed
// by a newline, suitable for an operator to copy to external storage.
func MarshalCheckpoint(checkpoint Checkpoint) ([]byte, error) {
	if err := verifyCheckpointShape(checkpoint); err != nil {
		return nil, err
	}
	if checkpoint.Signature == "" {
		return nil, errors.New("audit checkpoint signature is required")
	}
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return nil, fmt.Errorf("marshal audit checkpoint: %w", err)
	}
	return append(data, '\n'), nil
}

// KeyIDForPublicKey derives a non-secret stable identifier for a pinned key.
func KeyIDForPublicKey(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", errors.New("audit checkpoint requires a 32-byte Ed25519 public key")
	}
	digest := sha256.Sum256(publicKey)
	return "audit-" + hex.EncodeToString(digest[:16]), nil
}

func rejectDuplicateCheckpointFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode audit checkpoint: %w", err)
	}
	if token != json.Delim('{') {
		return errors.New("audit checkpoint must be one JSON object")
	}
	seen := make(map[string]struct{}, 7)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode audit checkpoint field: %w", err)
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("audit checkpoint field name must be a string")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("audit checkpoint repeats field %q", name)
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("decode audit checkpoint field %q: %w", name, err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("decode audit checkpoint end: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("audit checkpoint contains trailing JSON")
	}
	return nil
}

func verifyCheckpointSignature(checkpoint Checkpoint, publicKey ed25519.PublicKey) error {
	if err := verifyCheckpointShape(checkpoint); err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("pinned audit checkpoint public key must be 32 bytes")
	}
	signature, err := base64.StdEncoding.DecodeString(checkpoint.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("audit checkpoint signature is not valid base64 Ed25519")
	}
	payload, err := marshalCheckpointPayload(checkpoint)
	if err != nil {
		return err
	}
	message := make([]byte, 0, len(checkpointDomain)+len(payload))
	message = append(message, checkpointDomain...)
	message = append(message, payload...)
	if !ed25519.Verify(publicKey, message, signature) {
		return errors.New("audit checkpoint signature does not match the pinned public key")
	}
	return nil
}

func verifyCheckpointShape(checkpoint Checkpoint) error {
	if checkpoint.V != checkpointVersion || !validHex(checkpoint.LogID, 16) || checkpoint.Seq == 0 || !validHex(checkpoint.Hash, sha256.Size) {
		return errors.New("audit checkpoint has invalid version, log ID, sequence or hash")
	}
	if checkpoint.Time.IsZero() || checkpoint.Time.Location() != time.UTC {
		return errors.New("audit checkpoint time must be non-zero UTC")
	}
	if !validKeyID(checkpoint.KeyID) {
		return errors.New("audit checkpoint key ID is invalid")
	}
	if checkpoint.Signature != "" {
		signature, err := base64.StdEncoding.DecodeString(checkpoint.Signature)
		if err != nil || len(signature) != ed25519.SignatureSize {
			return errors.New("audit checkpoint signature is not valid base64 Ed25519")
		}
	}
	return nil
}

func marshalCheckpointPayload(checkpoint Checkpoint) ([]byte, error) {
	if err := verifyCheckpointShape(checkpoint); err != nil {
		return nil, err
	}
	return json.Marshal(checkpointPayload{
		V: checkpoint.V, LogID: checkpoint.LogID, Seq: checkpoint.Seq,
		Hash: checkpoint.Hash, Time: checkpoint.Time, KeyID: checkpoint.KeyID,
	})
}

func validKeyID(value string) bool {
	if len(value) == 0 || len(value) > maxKeyIDLength {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}
