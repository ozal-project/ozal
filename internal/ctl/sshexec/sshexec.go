// Package sshexec holds the operator-side adapters of internal/ctl: the system
// ssh client and a git+go builder.
package sshexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/octaviosantacruz/switchboard/internal/ctl"
)

// Remote runs commands on an edge through the system ssh client. The
// operator's own keys and known_hosts are the only trust: nothing is stored.
type Remote struct {
	Target      string
	Key         string
	Interactive bool // whether stdin is a terminal (sudo can prompt)
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

func (s Remote) base(extra ...string) []string {
	args := []string{"-i", expandHome(s.Key), "-o", "ConnectTimeout=10"}
	return append(append(args, extra...), s.Target)
}

// Run implements ctl.Remote.
func (s Remote) Run(ctx context.Context, command string, stdin io.Reader) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh", append(s.base("-o", "BatchMode=yes"), command)...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("remote command failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Terminal implements ctl.Remote.
func (s Remote) Terminal(ctx context.Context, command string) (int, error) {
	if !s.Interactive {
		return 0, errors.New("no terminal")
	}
	cmd := exec.CommandContext(ctx, "ssh", append(s.base("-t"), command)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), nil
		}
		return 255, nil
	}
	return 0, nil
}

// GitBuilder builds a committed ref of a Switchboard checkout for linux/amd64.
// It builds from `git archive`, so uncommitted files never reach an edge.
type GitBuilder struct {
	Repo    string // checkout of Switchboard
	WorkDir string // on a real disk: /tmp can be a small tmpfs
	GOOS    string
	GOARCH  string
}

// Build implements ctl.Builder.
func (b GitBuilder) Build(ctx context.Context, ref string) (ctl.Binary, error) {
	git := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", b.Repo}, args...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
	short, err := git("rev-parse", "--short", ref+"^{commit}")
	if err != nil {
		return ctl.Binary{}, err
	}
	commit := strings.TrimSpace(string(short))
	// A binary without `upgrade` would start a server as root: never ship one.
	if _, err := git("cat-file", "-e", commit+":internal/upgrade/upgrade.go"); err != nil {
		return ctl.Binary{}, fmt.Errorf("%s predates `switchboard upgrade`: build a newer ref", commit)
	}
	if err := os.MkdirAll(b.WorkDir, 0o755); err != nil {
		return ctl.Binary{}, err
	}
	work, err := os.MkdirTemp(b.WorkDir, "build-")
	if err != nil {
		return ctl.Binary{}, err
	}
	defer os.RemoveAll(work)
	src := filepath.Join(work, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		return ctl.Binary{}, err
	}
	archive, err := git("archive", commit)
	if err != nil {
		return ctl.Binary{}, err
	}
	untar := exec.CommandContext(ctx, "tar", "-x", "-C", src)
	untar.Stdin = bytes.NewReader(archive)
	if out, err := untar.CombinedOutput(); err != nil {
		return ctl.Binary{}, fmt.Errorf("unpack: %v: %s", err, out)
	}
	exe := filepath.Join(work, "switchboard")
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false",
		"-ldflags", "-X main.buildCommit="+commit, "-o", exe, "./cmd/switchboard")
	build.Dir = src
	build.Env = append(os.Environ(), "GOOS="+b.GOOS, "GOARCH="+b.GOARCH, "CGO_ENABLED=0", "GOTMPDIR="+work)
	if out, err := build.CombinedOutput(); err != nil {
		return ctl.Binary{}, fmt.Errorf("go build: %v\n%s", err, out)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return ctl.Binary{}, err
	}
	return ctl.Binary{Data: data, Commit: commit}, nil
}
