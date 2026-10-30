package ctl

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ozal-project/ozal/internal/config"
)

// Remote is the edge as the orchestration sees it. The SSH adapter implements
// it; tests use a fake.
type Remote interface {
	// Run executes a command and returns its stdout; stdin may be nil.
	Run(ctx context.Context, command string, stdin io.Reader) (string, error)
	// Terminal runs a command with the operator's terminal (sudo prompts) and
	// returns its exit status; 255 means SSH itself failed. It returns an
	// error only when no terminal is available.
	Terminal(ctx context.Context, command string) (int, error)
	// Stream runs a command without a terminal, its output going to the
	// operator, and returns its exit status; 255 means SSH itself failed.
	Stream(ctx context.Context, command string) (int, error)
}

// Binary is a built Ozal executable.
type Binary struct {
	Data   []byte
	Commit string
}

// Builder produces the binary to ship.
type Builder interface {
	Build(ctx context.Context, ref string) (Binary, error)
}

// UpgradeRequest is one run of `ozalctl edge upgrade`.
type UpgradeRequest struct {
	Name   string
	Target Target
	Ref    string
	Apply  bool
	// Owner names this run in the edge lock ("ozalctl on host pid N since T").
	Owner string
}

// ErrBusy is returned when another deployment holds the edge lock.
var ErrBusy = errors.New("another deployment holds the edge lock")

// ErrTerminalRequired means the candidate was staged but no cutover occurred.
var ErrTerminalRequired = errors.New("an interactive terminal is required to complete the upgrade")

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Upgrade builds, stages, validates and cuts over one edge, in the same order
// the earlier scripts used, under the edge lock from staging to the end.
func Upgrade(ctx context.Context, out io.Writer, remote Remote, builder Builder, req UpgradeRequest) error {
	e := req.Target.Edge
	step := func(format string, a ...any) { fmt.Fprintf(out, "==> "+format+"\n", a...) }
	note := func(format string, a ...any) { fmt.Fprintf(out, "    "+format+"\n", a...) }

	step("build %s", req.Ref)
	bin, err := builder.Build(ctx, req.Ref)
	if err != nil {
		return err
	}
	want := sha(bin.Data)
	note("%s: sha256 %s… (%d bytes)", bin.Commit, want[:16], len(bin.Data))

	live, err := remote.Run(ctx, "sha256sum "+Quote(e.Binary), nil)
	if err != nil {
		return err
	}
	liveSHA := strings.Fields(live)
	if len(liveSHA) == 0 {
		return errors.New("the edge did not report the installed binary's hash")
	}
	note("edge runs sha256 %s…", liveSHA[0][:min(16, len(liveSHA[0]))])
	if liveSHA[0] == want {
		step("the edge already runs this binary: nothing to do")
		return nil
	}
	backup := BackupName(e.Binary, liveSHA[0])
	if !req.Apply {
		step("plan: install %s (%s), keep %s, restart %s", bin.Commit, want[:12], backup, e.Unit)
		note("re-run with -apply to do it")
		return nil
	}

	got, err := remote.Run(ctx, LockAcquire(e.Lock, req.Owner), nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(got) != "taken" {
		return fmt.Errorf("%w (%s); if that process is gone, remove %s by hand after checking the edge", ErrBusy, strings.TrimSpace(got), e.Lock)
	}
	defer func() {
		// Released with a fresh context: an interrupted run must still let go.
		_, _ = remote.Run(context.WithoutCancel(ctx), LockRelease(e.Lock, req.Owner), nil)
	}()

	if _, err := remote.Run(ctx, "umask 077; mkdir -p "+Quote(e.Stage), nil); err != nil {
		return err
	}
	part, final := StageFile(e)
	_ = part
	staged := ""
	if out, err := remote.Run(ctx, "sha256sum "+Quote(final)+" 2>/dev/null || true", nil); err == nil {
		if f := strings.Fields(out); len(f) > 0 {
			staged = f[0]
		}
	}
	if staged == want {
		step("ozal.next already staged with this hash: no upload")
	} else {
		step("upload ozal.next")
		packed, err := gzipBytes(bin.Data)
		if err != nil {
			return err
		}
		note("gzip %d KiB -> %d KiB", len(bin.Data)/1024, len(packed)/1024)
		reply, err := remote.Run(ctx, ReceiveCommand(e), bytes.NewReader(packed))
		if err != nil {
			_, _ = remote.Run(context.WithoutCancel(ctx), "rm -f "+Quote(part), nil)
			return err
		}
		f := strings.Fields(reply)
		if len(f) == 0 || f[0] != want {
			_, _ = remote.Run(context.WithoutCancel(ctx), "rm -f "+Quote(final), nil)
			return fmt.Errorf("hash mismatch after upload: staged file does not match the build")
		}
	}

	step("validate the live config with the new binary")
	liveConfig, err := remote.Run(ctx, "cat "+Quote(e.Config), nil)
	if err != nil {
		return err
	}
	check, split, err := RootCheck([]byte(liveConfig))
	if err != nil {
		return err
	}
	target := e.Config
	if split {
		target = e.Stage + "/config.check"
		if _, err := remote.Run(ctx, "umask 077; cat > "+Quote(target), bytes.NewReader(check)); err != nil {
			return err
		}
	}
	checked, err := remote.Run(ctx, Quote(final)+" validate -config "+Quote(target), nil)
	if split {
		_, _ = remote.Run(context.WithoutCancel(ctx), "rm -f "+Quote(target), nil)
	}
	if err != nil {
		return fmt.Errorf("the new binary refuses the live config: %w", err)
	}
	note("%s", strings.TrimSpace(checked))
	if split {
		note("routes live in the runtime file only the service account reads: the root step validates them before it stops anything")
	}

	step("ozal upgrade on the edge as root (sudo asks for the password once)")
	code, err := remote.Terminal(ctx, UpgradeCommand(e, final, want))
	if err != nil {
		step("staged and validated; no terminal for sudo, so nothing was migrated, swapped or restarted")
		note("run the same command from a terminal to finish")
		return fmt.Errorf("%w: %v", ErrTerminalRequired, err)
	}
	if err := Outcome(code, e, backup); err != nil {
		return err
	}
	_, _ = remote.Run(ctx, "rm -f "+Quote(final), nil)
	step("%s is active with %s; previous binary kept as %s", e.Unit, bin.Commit, backup)
	return nil
}

// RootCheck returns what the SSH user can validate of the live config. With
// runtime_file, routes and projects live in a file only the service account
// reads, so the copy leaves it out: the user checks listeners, TLS and
// identity, and `ozal upgrade` validates the whole config as root
// before it stops anything. split is false when the live config is checked
// as it is.
func RootCheck(live []byte) (check []byte, split bool, err error) {
	var cfg config.Config
	decoder := json.NewDecoder(bytes.NewReader(live))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, false, fmt.Errorf("the live config does not parse: %w", err)
	}
	if cfg.RuntimeFile == "" {
		return nil, false, nil
	}
	cfg.RuntimeFile = ""
	check, err = marshal(cfg)
	return check, true, err
}

// gzipBytes compresses at the best level with no timestamp, so the same
// binary always produces the same upload.
func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
