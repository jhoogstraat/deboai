// Package config reads runtime configuration from the process environment and
// from optional environment files stored in the inspected repository.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvFileVariable overrides the environment files that LoadEnvFiles reads. It
// accepts a list separated by the platform path separator.
const EnvFileVariable = "DEBOAI_ENV_FILE"

// defaultEnvFiles are read, in order, from the repository root.
var defaultEnvFiles = []string{".env", "ci.env"}

// LoadEnvFiles reads the repository environment files into the process
// environment. Variables that are already set are never overwritten, and
// missing files are ignored.
func LoadEnvFiles(root string) error {
	for _, name := range envFiles() {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if err := loadEnvFile(path); err != nil {
			return err
		}
	}
	return nil
}

func envFiles() []string {
	configured := strings.TrimSpace(os.Getenv(EnvFileVariable))
	if configured == "" {
		return defaultEnvFiles
	}
	return filepath.SplitList(configured)
}

func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read environment file %s: %w", path, err)
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validKey(key) {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, unquote(strings.TrimSpace(value))); err != nil {
			return fmt.Errorf("set environment variable %s: %w", key, err)
		}
	}
	return nil
}

func unquote(value string) string {
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	return value
}

func validKey(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			index > 0 && character >= '0' && character <= '9',
			index > 0 && character == '_':
		default:
			return false
		}
	}
	return true
}

// Value returns the first non-empty value of the named variables.
func Value(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

// ValueOr returns the first non-empty value of the named variables, or fallback.
func ValueOr(fallback string, names ...string) string {
	if value := Value(names...); value != "" {
		return value
	}
	return fallback
}

// Require returns the first non-empty value of the named variables and fails
// when none of them is set.
func Require(names ...string) (string, error) {
	if value := Value(names...); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("missing environment variable: %s", strings.Join(names, " or "))
}

// List splits the first non-empty value of the named variables on commas.
func List(names ...string) []string {
	value := Value(names...)
	if value == "" {
		return nil
	}
	items := make([]string, 0)
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// Pairs parses the first non-empty value of the named variables as a
// comma-separated list of key=value entries.
func Pairs(names ...string) map[string]string {
	entries := List(names...)
	if len(entries) == 0 {
		return nil
	}
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		result[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return result
}
