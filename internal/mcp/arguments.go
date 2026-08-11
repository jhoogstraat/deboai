package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jhoogstraat/deboai/internal/tools"
)

// decodeArguments validates raw call arguments against a tool's input schema.
// Every declared property is a string, so unknown, non-string, and missing
// required arguments are all rejected before the handler runs.
func decodeArguments(schema map[string]any, raw map[string]json.RawMessage) (tools.Arguments, error) {
	properties, _ := schema["properties"].(map[string]any)
	arguments := make(tools.Arguments, len(raw))

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
