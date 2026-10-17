package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	secretEnvPrefix  = "env:"
	secretFilePrefix = "file:"
)

// ResolveSecret resolves a secret config value:
//
//   - "env:NAME"    reads environment variable NAME (must be non-empty);
//   - "file:/path"  reads the file and trims surrounding whitespace;
//   - anything else is an inline secret (local-only; discouraged for deploys).
//
// The prefixes are reserved: a secret whose first characters are "env:" or
// "file:" must be referenced or escaped by using a file.
func ResolveSecret(value string) (string, error) {
	switch {
	case strings.HasPrefix(value, secretEnvPrefix):
		name := strings.TrimSpace(strings.TrimPrefix(value, secretEnvPrefix))
		if name == "" {
			return "", fmt.Errorf("secret reference %q names no environment variable", value)
		}
		resolved := os.Getenv(name)
		if resolved == "" {
			return "", fmt.Errorf("secret environment variable %q is not set", name)
		}
		return resolved, nil
	case strings.HasPrefix(value, secretFilePrefix):
		path := strings.TrimSpace(strings.TrimPrefix(value, secretFilePrefix))
		if path == "" {
			return "", fmt.Errorf("secret reference %q names no file", value)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read secret file: %w", err)
		}
		resolved := strings.TrimSpace(string(data))
		if resolved == "" {
			return "", fmt.Errorf("secret file %q is empty", path)
		}
		return resolved, nil
	default:
		return value, nil
	}
}

// ValidateSecretReference checks the shape of a secret value without resolving
// it: validation must never read the secret. Inline values are accepted as-is.
func ValidateSecretReference(field, value string) error {
	switch {
	case strings.HasPrefix(value, secretEnvPrefix):
		if strings.TrimSpace(strings.TrimPrefix(value, secretEnvPrefix)) == "" {
			return fmt.Errorf("%s %q names no environment variable", field, value)
		}
	case strings.HasPrefix(value, secretFilePrefix):
		if strings.TrimSpace(strings.TrimPrefix(value, secretFilePrefix)) == "" {
			return fmt.Errorf("%s %q names no file", field, value)
		}
	}
	return nil
}
