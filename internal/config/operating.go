package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// operatingConfig is the complete mutable projection. It cannot alter host
// listeners, authentication, admin authority, certificate or database settings.
type operatingConfig struct {
	Routes   []Route    `json:"routes"`
	Projects []Project  `json:"projects"`
	GitHub   *GitHubApp `json:"github"`
}

func loadOperating(base *Config, root []byte) (*Config, string, error) {
	if base.RuntimeFile == "" {
		return base, Digest(root), nil
	}
	raw, err := os.ReadFile(base.RuntimeFile)
	if err != nil {
		return nil, "", fmt.Errorf("read operational configuration: %w", err)
	}
	var operational operatingConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&operational); err != nil {
		return nil, "", fmt.Errorf("invalid operational configuration: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, "", fmt.Errorf("invalid operational configuration: multiple values")
	}
	cfg := Clone(*base)
	cfg.Routes = operational.Routes
	cfg.Projects = operational.Projects
	cfg.GitHub = operational.GitHub
	validated, err := Validate(cfg)
	if err != nil {
		return nil, "", err
	}
	combined := append(append(append([]byte{}, root...), []byte("\x00operating\x00")...), raw...)
	return validated, Digest(combined), nil
}

// PersistOperating writes only operational state when configured, retaining
// the legacy single-file behavior otherwise. Secret references remain references.
func PersistOperating(rootPath string, cfg Config) error {
	if cfg.RuntimeFile == "" {
		return WriteAtomic(rootPath, cfg)
	}
	if err := ValidateOperatingPath(rootPath, cfg.RuntimeFile); err != nil {
		return err
	}
	data, err := OperatingJSON(cfg)
	if err != nil {
		return err
	}
	return writeBytesAtomic(cfg.RuntimeFile, data)
}

// OperatingJSON prepares an explicitly initialized operational projection.
// It retains references and has no filesystem or secret-resolution side effects.
func OperatingJSON(cfg Config) ([]byte, error) {
	validated, err := Validate(cfg)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(operatingConfig{Routes: validated.Routes, Projects: validated.Projects, GitHub: validated.GitHub}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// ValidateOperatingPath prevents an operational write from replacing its root
// through an exact path, symlink, symlinked parent or existing hard link.
func ValidateOperatingPath(rootPath, runtimePath string) error {
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return err
	}
	runtime, err := filepath.Abs(runtimePath)
	if err != nil {
		return err
	}
	if root == runtime {
		return fmt.Errorf("runtime_file must differ from the root configuration")
	}
	rootResolved, rootErr := filepath.EvalSymlinks(root)
	runtimeResolved, runtimeErr := filepath.EvalSymlinks(runtime)
	if rootErr == nil && runtimeErr == nil && rootResolved == runtimeResolved {
		return fmt.Errorf("runtime_file cannot alias the root configuration")
	}
	rootInfo, rootStatErr := os.Stat(root)
	runtimeInfo, runtimeStatErr := os.Stat(runtime)
	if rootStatErr == nil && runtimeStatErr == nil && os.SameFile(rootInfo, runtimeInfo) {
		return fmt.Errorf("runtime_file cannot alias the root configuration")
	}
	return nil
}
