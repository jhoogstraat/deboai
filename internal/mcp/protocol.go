// Package mcp implements the stdio Model Context Protocol server that exposes
// the development tools to an assistant.
package mcp

import (
	"encoding/json"

	"github.com/jhoogstraat/deboai/internal/tools"
)

// CurrentProtocol is the protocol revision this server speaks by default.
const CurrentProtocol = "2026-07-28"

// SupportedProtocols lists the revisions accepted during initialization, oldest
// first. Revisions are dated, so the slice order matches string order.
var SupportedProtocols = []string{
	"2024-11-05",
	"2025-03-26",
	"2025-06-18",
	"2025-11-25",
	CurrentProtocol,
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
type Handler = tools.Handler

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

// chooseProtocolVersion answers an initialize request with the newest revision
// this server supports that the client is not older than. Clients reject any
// answer they do not know themselves, so replying with CurrentProtocol to a
// client that asked for an older revision would fail the handshake outright.
func chooseProtocolVersion(requested string) string {
	if requested == "" {
		return CurrentProtocol
	}
	for i := len(SupportedProtocols) - 1; i >= 0; i-- {
		if SupportedProtocols[i] <= requested {
			return SupportedProtocols[i]
		}
	}
	// Older than anything we speak: offer the oldest revision we have.
	return SupportedProtocols[0]
}
