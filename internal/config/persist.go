package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic persists cfg with a same-directory rename, so readers observe
// either the old complete document or the new complete document.
func WriteAtomic(path string, cfg Config) (err error) {
	normalized, err := Validate(cfg)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	// Replace the symlink's target, not the link itself: an operator's config
	// path may be a symlink (e.g. to a versioned file) and must stay one.
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	} else if !os.IsNotExist(resolveErr) {
		return fmt.Errorf("resolve config path: %w", resolveErr)
	}

	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("stat config: %w", statErr)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".switchboard-config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		tmp.Close()
		if err != nil {
			os.Remove(tmpPath)
		}
	}()

	if err = tmp.Chmod(mode); err != nil {
		return fmt.Errorf("set temporary config permissions: %w", err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	// A rename is only durable once the directory entry is synced. Not every
	// platform supports directory fsync, so a failure here is not fatal.
	if dirFile, openErr := os.Open(dir); openErr == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}

	return nil
}
