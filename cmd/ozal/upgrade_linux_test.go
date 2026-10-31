//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunningChecksTheLiveProcessEvenWhenTheInstalledPathChanges(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	path := filepath.Join(t.TempDir(), "ozal")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hash), source); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	want := hex.EncodeToString(hash.Sum(nil))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, path)
	child.Env = append(os.Environ(), "OZAL_TEST_LOCK_PROCESS=image")
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); _ = child.Process.Kill(); _ = child.Wait() }()
	if err := os.Rename(path, path+".previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new installed image, not running"), 0700); err != nil {
		t.Fatal(err)
	}
	s := &upgradeSystem{pid: func(context.Context) (int, error) { return child.Process.Pid, nil }}
	if !s.Running(ctx, want) {
		t.Fatal("live process image was not verified")
	}
	wrong := sha256.Sum256([]byte("new installed image, not running"))
	if s.Running(ctx, hex.EncodeToString(wrong[:])) {
		t.Fatal("installed file was mistaken for the active process")
	}
	calls := 0
	s.pid = func(context.Context) (int, error) {
		calls++
		if calls > 1 {
			return child.Process.Pid + 1, nil
		}
		return child.Process.Pid, nil
	}
	if s.Running(ctx, want) {
		t.Fatal("MainPID changed during verification but was accepted")
	}
}

func TestCandidateVersionMustMatchTheReviewedRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidate")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '{\"name\":\"ozal\",\"version\":\"0.2.0\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkCandidateVersion(context.Background(), path, "v0.2.0"); err != nil {
		t.Fatal(err)
	}
	if err := checkCandidateVersion(context.Background(), path, "v0.3.0"); err == nil || !strings.Contains(err.Error(), "reviewed release") {
		t.Fatalf("mislabeled release err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := checkCandidateVersion(ctx, path, "v0.2.0"); err == nil {
		t.Fatal("cancelled version check succeeded")
	}
}
