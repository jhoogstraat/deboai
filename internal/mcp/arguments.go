package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Arguments holds the validated string arguments of a tool call.
type Arguments map[string]string

// String returns the argument, or the empty string when it was omitted.
func (a Arguments) String(name string) string {
	return a[name]
}

// decodeArguments validates raw call arguments against a tool's input schema.
// Every declared property is a string, so unknown, non-string, and missing
// required arguments are all rejected before the handler runs.
func decodeArguments(schema map[string]any, raw map[string]json.RawMessage) (Arguments, error) {
	properties, _ := schema["properties"].(map[string]any)
	arguments := make(Arguments, len(raw))

	for _, name := range sortedNames(raw) {
		if _, declared := properties[name]; !declared {
			return nil, fmt.Errorf("unexpected tool argument: %s", name)
		}
		var value string
		if err := json.Unmarshal(raw[name], &value); err != nil {
			return nil, fmt.Errorf("argument %s must be a string", name)
		}
		arguments[name] = value
	}

	for _, name := range requiredNames(schema) {
		if strings.TrimSpace(arguments[name]) == "" {
			if _, ok := arguments[name]; !ok {
				return nil, fmt.Errorf("missing required argument: %s", name)
			}
			return nil, fmt.Errorf("argument %s must not be empty", name)
		}
	}
	return arguments, nil
}

func requiredNames(schema map[string]any) []string {
	values, _ := schema["required"].([]string)
	return values
}

func sortedNames(raw map[string]json.RawMessage) []string {
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
