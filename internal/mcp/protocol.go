// Package mcp implements the stdio Model Context Protocol server that exposes
// the development tools to an assistant.
package mcp

import (
	"context"
	"encoding/json"
)

// CurrentProtocol is the protocol revision this server speaks by default.
const CurrentProtocol = "2025-06-18"

// SupportedProtocols lists the revisions accepted during initialization.
var SupportedProtocols = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// JSON-RPC error codes used by this server.
const (
	codeParseError     = -32700
	codeInvalidParams  = -32602
	codeMethodNotFound = -32601
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type toolCallParams struct {
	Name      string                     `json:"name"`
	Arguments map[string]json.RawMessage `json:"arguments"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// Handler runs a tool and returns the text handed back to the assistant.
type Handler func(ctx context.Context, arguments Arguments) (string, error)

// Tool is a single callable exposed through tools/list and tools/call.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Handler     Handler        `json:"-"`
}

// Info identifies the server during initialization.
type Info struct {
	Name         string
	Version      string
	Instructions string
}

// ObjectSchema builds a strict JSON schema object for a tool's arguments.
func ObjectSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// StringProperty describes a single string argument.
func StringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func chooseProtocolVersion(requested string) string {
	if SupportedProtocols[requested] {
		return requested
	}
	return CurrentProtocol
}
