package mcp

import "github.com/jhoogstraat/deboai/internal/tools"

// Adapt exposes tool definitions through the Model Context Protocol.
func Adapt(definitions []tools.Definition) []Tool {
	adapted := make([]Tool, 0, len(definitions))
	for _, definition := range definitions {
		properties := make(map[string]any, len(definition.Arguments))
		required := make([]string, 0, len(definition.Arguments))
		for _, argument := range definition.Arguments {
			properties[argument.Name] = StringProperty(argument.Description)
			if argument.Required {
				required = append(required, argument.Name)
			}
		}
		adapted = append(adapted, Tool{
			Name:        definition.Name,
			Description: definition.Description,
			InputSchema: ObjectSchema(properties, required...),
			Handler:     definition.Handler,
		})
	}
	return adapted
}
