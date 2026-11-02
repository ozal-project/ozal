package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/ozal-project/ozal/internal/pairhelper"
)

func runPairHelper(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("pair-helper accepts requests on stdin and no command-line arguments")
	}
	if os.Geteuid() != 0 {
		return errors.New("pair-helper must be invoked through its restricted sudo rule")
	}
	profileFile, err := os.Lstat(pairhelper.ProfilePath)
	if err != nil {
		return fmt.Errorf("read pair helper profile: %w", err)
	}
	if !profileFile.Mode().IsRegular() || profileFile.Mode()&os.ModeSymlink != 0 || profileFile.Mode().Perm()&0o022 != 0 {
		return errors.New("pair helper profile must be a regular root-owned file, not writable by group or others")
	}
	stat, ok := profileFile.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return errors.New("pair helper profile must be owned by root")
	}
	file, err := os.Open(pairhelper.ProfilePath)
	if err != nil {
		return fmt.Errorf("open pair helper profile: %w", err)
	}
	profile, decodeErr := pairhelper.DecodeProfile(file)
	closeErr := file.Close()
	if decodeErr != nil || closeErr != nil {
		return errors.Join(decodeErr, closeErr)
	}
	request, err := pairhelper.DecodeRequest(os.Stdin)
	if err != nil {
		return err
	}
	runner := pairhelper.Runner{Profile: profile, Stdout: out, Stderr: os.Stderr}
	return runner.Run(ctx, request)
}
