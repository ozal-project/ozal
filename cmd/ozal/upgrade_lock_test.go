//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/updatelock"
)

// These helpers simulate only an installation's lifetime; they never replace
// a binary, open a real config, or invoke systemctl.
func TestMain(m *testing.M) {
	if mode := os.Getenv("OZAL_TEST_LOCK_PROCESS"); mode != "" {
		if err := runLockProcess(mode); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runLockProcess(mode string) error {
	if mode == "image" {
		_, err := io.Copy(io.Discard, os.Stdin)
		return err
	}
	path := os.Getenv("OZAL_TEST_LOCK_PATH")
	if mode == "parent" {
		lock, err := updatelock.Acquire(path)
		if err != nil {
			return err
		}
		defer lock.Release()
		if err := os.Setenv("OZAL_TEST_LOCK_PROCESS", "child"); err != nil {
			return err
		}
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		return systemUpdateEnv().upgrade(context.Background(), binary, nil, lock, os.Stdout)
	}
	if mode != "child" {
		return errors.New("unknown lock helper mode")
	}
	fd, err := strconv.ParseUint(os.Getenv(updatelock.EnvFD), 10, 32)
	if err != nil {
		return fmt.Errorf("child received no lock descriptor: %w", err)
	}
	lock, err := updatelock.Inherit(path, uintptr(fd))
	if err != nil {
		return err
	}
	defer lock.Release()
	if _, err := fmt.Fprintln(os.Stdout, os.Getpid()); err != nil {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path + ".finish"); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("lock child timed out waiting for the simulated upgrade to finish")
}

func TestUpgradeChildKeepsLockAfterUpdateParentIsKilled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.CommandContext(ctx, binary)
	parent.Env = append(os.Environ(), "OZAL_TEST_LOCK_PROCESS=parent", "OZAL_TEST_LOCK_PATH="+path)
	parent.Stderr = os.Stderr
	stdout, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = parent.Process.Kill()
			_ = parent.Wait()
		}
	})
	type ready struct {
		pid int
		err error
	}
	started := make(chan ready, 1)
	go func() {
		var pid int
		_, err := fmt.Fscanln(bufio.NewReader(stdout), &pid)
		started <- ready{pid, err}
	}()
	var child *os.Process
	select {
	case result := <-started:
		if result.err != nil || result.pid <= 0 {
			t.Fatalf("child did not adopt the lock: pid=%d err=%v", result.pid, result.err)
		}
		child, err = os.FindProcess(result.pid)
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for upgrade child")
	}
	t.Cleanup(func() { _ = child.Kill() })
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	waited = true
	if second, err := updatelock.Acquire(path); !errors.Is(err, updatelock.ErrBusy) {
		if second != nil {
			_ = second.Release()
		}
		t.Fatalf("parent died while upgrade child was alive: second lock err=%v, want ErrBusy", err)
	}
	if err := os.WriteFile(path+".finish", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		lock, err := updatelock.Acquire(path)
		if err == nil {
			_ = lock.Release()
			return
		}
		if !errors.Is(err, updatelock.ErrBusy) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("lock was not released when the upgrade child finished")
}

func TestUpgradeRejectsInvalidInheritedLockBeforeReadingConfig(t *testing.T) {
	for _, descriptor := range []string{"", "not-a-descriptor", "0", "1", "2", "2147483647"} {
		t.Run(descriptor, func(t *testing.T) {
			t.Setenv(updatelock.EnvFD, descriptor)
			dir := t.TempDir()
			err := runUpgrade(context.Background(), []string{
				"-unit", "ozal", "-binary", filepath.Join(dir, "ozal"),
				"-sha256", "unused", "-config", filepath.Join(dir, "missing-config.json"),
			}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "update lock") {
				t.Fatalf("descriptor %q: err=%v, want lock refusal", descriptor, err)
			}
		})
	}
}
