// Package jsonutil holds helpers for reading and compacting the loosely typed
// JSON documents returned by the development tool APIs.
package jsonutil

import (
	"encoding/json"
	"fmt"
	"maps"
)

// String returns value when it is a string and the empty string otherwise.
func String(value any) string {
	parsed, _ := value.(string)
	return parsed
}

// Map returns value when it is a JSON object and nil otherwise.
func Map(value any) map[string]any {
	parsed, _ := value.(map[string]any)
	return parsed
}

// Array returns the array stored under key, or nil when it is absent.
func Array(value map[string]any, key string) []any {
	values, _ := value[key].([]any)
	return values
}

// FirstNonNil returns the first value that is not nil.
func FirstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// FirstNonEmpty returns the first value that is neither nil nor an empty string.
func FirstNonEmpty(values ...any) any {
	for _, value := range values {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && text == "" {
			continue
		}
		return value
	}
	return nil
}

// FirstString returns the first value that is a non-empty string.
func FirstString(values ...any) string {
	for _, value := range values {
		if result := String(value); result != "" {
			return result
		}
	}
	return ""
}

// FirstText returns the first value that formats into a non-empty string.
func FirstText(values ...any) string {
	for _, value := range values {
		if value == nil {
			continue
		}
		if text := fmt.Sprint(value); text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

// IsEmpty reports whether value is nil, an empty string, or an empty container.
func IsEmpty(value any) bool {
	if value == nil {
		return true
	}
	switch parsed := value.(type) {
	case string:
		return parsed == ""
	case []any:
		return len(parsed) == 0
	case map[string]any:
		return len(parsed) == 0
	default:
		return false
	}
}

// RemoveEmpty returns a copy of value without its empty entries.
func RemoveEmpty(value map[string]any) map[string]any {
	result := map[string]any{}
	for key, item := range value {
		if !IsEmpty(item) {
			result[key] = item
		}
	}
	return result
}

// Merge returns a new map containing first overlaid with second.
func Merge(first, second map[string]any) map[string]any {
	result := make(map[string]any, len(first)+len(second))
	maps.Copy(result, first)
	maps.Copy(result, second)
	return result
}

// Nullable returns nil for the empty string so it encodes as JSON null.
func Nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// Compact encodes value as a single-line JSON document.
func Compact(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode JSON: %w", err)
	}
	return string(data), nil
}
