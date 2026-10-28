package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/switchboard-project/switchboard/internal/audit"
	"github.com/switchboard-project/switchboard/internal/config"
)

// runAudit dispatches offline audit maintenance commands. Local verification
// is distinct from verifying against a signed external checkpoint.
func runAudit(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return auditUsageError()
	}
	switch args[0] {
	case "keygen":
		return runAuditKeygen(ctx, args[1:], out)
	case "checkpoint":
		return runAuditCheckpoint(ctx, args[1:], out)
	case "repair":
		return runAuditRepair(ctx, args[1:], out)
	case "verify":
		return runAuditVerify(ctx, args[1:], out)
	default:
		return auditUsageError()
	}
}

func auditUsageError() error {
	return errors.New("usage: switchboard audit <keygen|checkpoint|repair|verify> [flags]")
}

func runAuditRepair(ctx context.Context, args []string, out io.Writer) error {
	fs := auditFlagSet("audit repair", out)
	configPath := fs.String("config", "config.json", "config path when -dir is omitted")
	dir := fs.String("dir", "", "audit directory (absolute path required)")
	apply := fs.Bool("apply", false, "apply the displayed tail repair and append an audit.repaired record")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	auditDir, err := auditDirectory(*dir, *configPath)
	if err != nil {
		return err
	}
	if !*apply {
		plan, err := audit.InspectRepair(ctx, auditDir)
		if err != nil {
			return fmt.Errorf("inspect audit tail: %w", err)
		}
		if !plan.Needed {
			fmt.Fprintf(out, "audit chain verified; no repair required (records=%d)\n", plan.VerifiedRecords)
			return nil
		}
		fmt.Fprintf(out, "repair required: segment=%06d action=%s offset=%d discarded_bytes=%d tail_sha256=%s verified_records=%d\n",
			plan.Segment, repairAction(plan), plan.Offset, plan.DiscardedBytes, plan.TailSHA256, plan.VerifiedRecords)
		fmt.Fprintln(out, "No data changed. Stop Switchboard, review the plan, then re-run with -apply.")
		return nil
	}
	result, err := audit.RepairDir(ctx, auditDir)
	if err != nil {
		return fmt.Errorf("repair audit tail: %w", err)
	}
	if !result.Plan.Needed {
		fmt.Fprintf(out, "audit chain verified; no repair required (records=%d)\n", result.Plan.VerifiedRecords)
		return nil
	}
	fmt.Fprintf(out, "audit repair applied and recorded: segment=%06d action=%s discarded_bytes=%d repair_record_seq=%d\n",
		result.Plan.Segment, repairAction(result.Plan), result.Plan.DiscardedBytes, result.RepairRecordSeq)
	return nil
}

func repairAction(plan audit.RepairPlan) string {
	if plan.RestoresRecord {
		return "restore_newline"
	}
	return "truncate_tail"
}

func runAuditKeygen(ctx context.Context, args []string, out io.Writer) error {
	if runtime.GOOS == "windows" {
		return audit.ErrUnsupportedPlatform
	}
	fs := auditFlagSet("audit keygen", out)
	privateOut := fs.String("private-out", "", "new private key file (absolute path, created mode 0600)")
	publicOut := fs.String("public-out", "", "new pinned public key file (absolute path)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *privateOut == "" || *publicOut == "" {
		return errors.New("usage: switchboard audit keygen -private-out ABSOLUTE_PATH -public-out ABSOLUTE_PATH")
	}
	privatePath, err := absoluteNewPath(*privateOut)
	if err != nil {
		return fmt.Errorf("private key path: %w", err)
	}
	publicPath, err := absoluteNewPath(*publicOut)
	if err != nil {
		return fmt.Errorf("public key path: %w", err)
	}
	if privatePath == publicPath {
		return errors.New("private and public key paths must differ")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate Ed25519 key: %w", err)
	}
	defer clear(privateKey)
	if err := ctx.Err(); err != nil {
		return err
	}
	keyID, err := audit.KeyIDForPublicKey(publicKey)
	if err != nil {
		return err
	}
	privateEncoded := base64.StdEncoding.AppendEncode(nil, privateKey)
	privateEncoded = append(privateEncoded, '\n')
	defer clear(privateEncoded)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeNewKey(privatePath, privateEncoded, 0o600, true); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(privatePath)
		return err
	}
	if err := writeNewKey(publicPath, []byte(base64.StdEncoding.EncodeToString(publicKey)+"\n"), 0o644, false); err != nil {
		_ = os.Remove(privatePath)
		return fmt.Errorf("write public key: %w", err)
	}
	fmt.Fprintf(out, "key_id: %s\nprivate key: %s (mode 0600; keep secret)\npinned public key: %s (copy to the verification environment)\n", keyID, privatePath, publicPath)
	return nil
}

func runAuditCheckpoint(ctx context.Context, args []string, out io.Writer) error {
	if runtime.GOOS == "windows" {
		return audit.ErrUnsupportedPlatform
	}
	fs := auditFlagSet("audit checkpoint", out)
	configPath := fs.String("config", "config.json", "config path when -dir is omitted")
	dir := fs.String("dir", "", "audit directory (absolute path required)")
	keyRef := fs.String("key", "", "private key secret reference (file:PATH or env:NAME)")
	keyID := fs.String("key-id", "", "checkpoint signing key ID (defaults to the public-key fingerprint)")
	output := fs.String("out", "", "new checkpoint file (absolute path, created mode 0600)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *keyRef == "" || *output == "" {
		return errors.New("usage: switchboard audit checkpoint -key file:PATH|env:NAME -out ABSOLUTE_PATH [-config FILE|-dir PATH] [-key-id ID]")
	}
	if !strings.HasPrefix(*keyRef, "file:") && !strings.HasPrefix(*keyRef, "env:") {
		return errors.New("-key must use file: or env:; inline private keys are not accepted")
	}
	outputPath, err := absoluteNewPath(*output)
	if err != nil {
		return fmt.Errorf("checkpoint output path: %w", err)
	}
	privateKey, err := resolveAuditPrivateKey(*keyRef)
	if err != nil {
		return err
	}
	defer clear(privateKey)
	if *keyID == "" {
		publicKey := privateKey.Public().(ed25519.PublicKey)
		*keyID, err = audit.KeyIDForPublicKey(publicKey)
		if err != nil {
			return err
		}
	}
	auditDir, err := auditDirectory(*dir, *configPath)
	if err != nil {
		return err
	}
	checkpoint, err := audit.CreateCheckpoint(ctx, auditDir, privateKey, *keyID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("create audit checkpoint: %w", err)
	}
	data, err := audit.MarshalCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	if err := writeNewKey(outputPath, data, 0o600, true); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	fmt.Fprintf(out, "checkpoint written: %s (copy the checkpoint and pinned public key outside the gateway host)\n", outputPath)
	return nil
}

func resolveAuditPrivateKey(reference string) (ed25519.PrivateKey, error) {
	if strings.HasPrefix(reference, "file:") {
		path := strings.TrimSpace(strings.TrimPrefix(reference, "file:"))
		if path == "" {
			return nil, errors.New("checkpoint private key file path is empty")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect checkpoint private key: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("checkpoint private key must be a regular file, not a symlink")
		}
		if info.Mode().Perm()&0o077 != 0 || info.Mode().Perm()&0o400 == 0 {
			return nil, errors.New("checkpoint private key file must be owner-only and owner-readable")
		}
	}
	value, err := config.ResolveSecret(reference)
	if err != nil {
		return nil, fmt.Errorf("resolve checkpoint key: %w", err)
	}
	privateBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(privateBytes) != ed25519.PrivateKeySize {
		clear(privateBytes)
		return nil, errors.New("checkpoint private key must be a base64-encoded Ed25519 private key")
	}
	return ed25519.PrivateKey(privateBytes), nil
}

func runAuditVerify(ctx context.Context, args []string, out io.Writer) error {
	fs := auditFlagSet("audit verify", out)
	configPath := fs.String("config", "config.json", "config path when -dir is omitted")
	dir := fs.String("dir", "", "audit directory to verify (absolute path required)")
	checkpointPath := fs.String("checkpoint", "", "signed checkpoint JSON (optional)")
	publicKeyPath := fs.String("public-key", "", "pinned Ed25519 public key file (required with -checkpoint)")
	asJSON := fs.Bool("json", false, "print stable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if (*checkpointPath == "") != (*publicKeyPath == "") {
		return errors.New("-checkpoint and -public-key must be supplied together")
	}
	auditDir, err := auditDirectory(*dir, *configPath)
	if err != nil {
		return err
	}
	var verification audit.Verification
	if *checkpointPath == "" {
		verification, err = audit.VerifyDir(ctx, auditDir)
	} else {
		checkpointData, readErr := readBoundedFile(*checkpointPath, 64<<10)
		if readErr != nil {
			return fmt.Errorf("read audit checkpoint: %w", readErr)
		}
		checkpoint, decodeErr := audit.DecodeCheckpoint(checkpointData)
		if decodeErr != nil {
			return decodeErr
		}
		publicData, readErr := readBoundedFile(*publicKeyPath, 4096)
		if readErr != nil {
			return fmt.Errorf("read pinned public key: %w", readErr)
		}
		publicBytes, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(publicData)))
		if decodeErr != nil || len(publicBytes) != ed25519.PublicKeySize {
			return errors.New("pinned public key must be a base64-encoded 32-byte Ed25519 public key")
		}
		verification, err = audit.VerifyDirWithCheckpoint(ctx, auditDir, checkpoint, ed25519.PublicKey(publicBytes))
	}
	if err != nil {
		return fmt.Errorf("verify audit chain: %w", err)
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(verification)
	}
	fmt.Fprintf(out, "audit chain internally consistent: records=%d log_id=%s last_hash=%s tamper_evident=%t\n%s\n",
		verification.Records, verification.LogID, verification.LastHash, verification.TamperEvident, verification.Note)
	return nil
}

func auditFlagSet(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	return fs
}

func auditDirectory(dir, configPath string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", err
	}
	if cfg.Audit == nil {
		return "", errors.New("audit is not configured; pass -dir or configure audit.dir")
	}
	return cfg.Audit.Dir, nil
}

func absoluteNewPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("absolute path required")
	}
	return filepath.Clean(path), nil
}

func writeNewKey(path string, data []byte, mode os.FileMode, requirePrivate bool) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if requirePrivate && (info.Mode().Perm()&0o077 != 0 || info.Mode().Perm()&0o400 == 0) {
		return errors.New("private output permissions must be owner-only with owner read access")
	}
	for len(data) > 0 {
		n, writeErr := file.Write(data)
		if writeErr != nil {
			return writeErr
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := syncAuditOutputDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func syncAuditOutputDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open output directory for sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync output directory: %w", err)
	}
	return nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}
