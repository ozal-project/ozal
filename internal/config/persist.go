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

	return nil
}
