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

// EnvFileVariable overrides the environment files that Load reads. It
// accepts a list separated by the platform path separator.
const EnvFileVariable = "DEBOAI_ENV_FILE"

// defaultEnvFiles are read, in order, from the repository root.
var defaultEnvFiles = []string{".env", "debo.env"}

// Values is a request-scoped environment.
type Values map[string]string

// Load reads the process environment and environment files from roots in order.
// Process values and values from earlier roots take precedence; neither the
// process environment nor other requests are modified.
func Load(roots ...string) (Values, error) {
	values := Values{}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	names := defaultEnvFiles
	if configured := strings.TrimSpace(values[EnvFileVariable]); configured != "" {
		names = filepath.SplitList(configured)
	}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		if err := loadEnvFiles(values, root, names); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func loadEnvFiles(values Values, root string, names []string) error {
	for _, name := range names {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if err := loadEnvFile(values, path); err != nil {
			return err
		}
	}
	return nil
}

func loadEnvFile(values Values, path string) error {
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
		if _, exists := values[key]; exists {
			continue
		}
		values[key] = unquote(strings.TrimSpace(value))
	}
	return nil
}

// Value returns the first non-empty value of the named variables.
func (v Values) Value(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(v[name]); value != "" {
			return value
		}
	}
	return ""
}

// ValueOr returns the first non-empty value of the named variables, or fallback.
func (v Values) ValueOr(fallback string, names ...string) string {
	if value := v.Value(names...); value != "" {
		return value
	}
	return fallback
}

// Require returns the first non-empty value of the named variables and fails
// when none of them is set.
func (v Values) Require(names ...string) (string, error) {
	if value := v.Value(names...); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("missing environment variable: %s", strings.Join(names, " or "))
}

// List splits the first non-empty value of the named variables on commas.
func (v Values) List(names ...string) []string { return list(v.Value(names...)) }

// Pairs parses the first non-empty value of the named variables as a
// comma-separated list of key=value entries.
func (v Values) Pairs(names ...string) map[string]string { return pairs(v.Value(names...)) }

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
	return list(Value(names...))
}

func list(value string) []string {
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
	return pairs(Value(names...))
}

func pairs(value string) map[string]string {
	entries := list(value)
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
