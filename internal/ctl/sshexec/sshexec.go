// Package sshexec holds the operator-side adapters of internal/ctl: the system
// ssh client and a git+go builder.
package sshexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/ctl"
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

// GitBuilder builds a committed ref of a Ozal checkout for linux/amd64.
// It builds from `git archive`, so uncommitted files never reach an edge.
type GitBuilder struct {
	Repo    string // checkout of Ozal
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
		return ctl.Binary{}, fmt.Errorf("%s predates `ozal upgrade`: build a newer ref", commit)
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
	exe := filepath.Join(work, "ozal")
	commandPath := "./cmd/ozal"
	if _, err := os.Stat(filepath.Join(src, "cmd", "ozal")); errors.Is(err, os.ErrNotExist) {
		// Historical rollback refs keep their original command directory.
		commandPath = "./cmd/switchboard"
	}
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false",
		"-ldflags", "-X main.buildCommit="+commit, "-o", exe, commandPath)
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

// Forward implements publish.Tunnel: `ssh -N -L` from a free loopback port to
// remote, an address on the edge. It returns once the port accepts
// connections; stop ends the tunnel.
func (s Remote) Forward(ctx context.Context, remote string) (string, func(), error) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	local := probe.Addr().String()
	probe.Close()
	tunnelCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(tunnelCtx, "ssh", s.base("-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes", "-N", "-L", local+":"+remote)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return "", nil, err
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	stop := func() { cancel(); <-exited }
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case <-exited:
			cancel()
			return "", nil, fmt.Errorf("ssh tunnel ended: %s", strings.TrimSpace(stderr.String()))
		default:
		}
		if conn, err := net.DialTimeout("tcp", local, 300*time.Millisecond); err == nil {
			conn.Close()
			return local, stop, nil
		}
		if time.Now().After(deadline) {
			stop()
			return "", nil, errors.New("ssh tunnel did not come up within 15 seconds")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ReadToken reads a token file on the operator's machine (~ expanded),
// refusing one other users can read.
func ReadToken(path string) (string, error) {
	path = expandHome(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is readable by other users; chmod 600 it", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return token, nil
}
