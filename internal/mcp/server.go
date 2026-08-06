package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// MaxMessageLength bounds a single JSON-RPC message read from stdin.
const MaxMessageLength = 16 * 1024 * 1024

// DefaultCallTimeout bounds a single tool invocation.
const DefaultCallTimeout = 5 * time.Minute

// Server serves the Model Context Protocol over a line-delimited JSON stream.
type Server struct {
	info        Info
	tools       []Tool
	index       map[string]Tool
	callTimeout time.Duration
}

// NewServer builds a server exposing the given tools in the order provided.
func NewServer(info Info, tools ...Tool) *Server {
	index := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		index[tool.Name] = tool
	}
	return &Server{info: info, tools: tools, index: index, callTimeout: DefaultCallTimeout}
}

// SetCallTimeout overrides how long a single tool call may run.
func (s *Server) SetCallTimeout(timeout time.Duration) {
	s.callTimeout = timeout
}

// Serve reads requests from input until it is exhausted, writing one JSON
// response per line to output.
func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), MaxMessageLength)
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		request, err := decodeRequest(line)
		if err != nil {
			if err := writeResponse(encoder, rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: codeParseError, Message: err.Error()},
			}); err != nil {
				return err
			}
			continue
		}

		result, rpcErr := s.handle(ctx, request)
		if len(request.ID) == 0 {
			continue
		}

		response := rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result}
		if rpcErr != nil {
			response.Result = nil
			response.Error = rpcErr
		}
		if err := writeResponse(encoder, response); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read MCP input: %w", err)
	}
	return nil
}

func decodeRequest(line []byte) (rpcRequest, error) {
	var request rpcRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return rpcRequest{}, fmt.Errorf("invalid JSON-RPC message: %w", err)
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		return rpcRequest{}, errors.New("invalid JSON-RPC request")
	}
	return request, nil
}

func writeResponse(encoder *json.Encoder, response rpcResponse) error {
	if len(response.ID) == 0 {
		response.ID = json.RawMessage("null")
	}
	return encoder.Encode(response)
}

func (s *Server) handle(ctx context.Context, request rpcRequest) (any, *rpcError) {
	switch request.Method {
	case "initialize":
		return s.initialize(request.Params), nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		return s.callTool(ctx, request.Params)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found"}
	}
}

func (s *Server) initialize(raw json.RawMessage) map[string]any {
	requested := ""
	if len(raw) > 0 {
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(raw, &params) == nil {
			requested = params.ProtocolVersion
		}
	}
	return map[string]any{
		"protocolVersion": chooseProtocolVersion(requested),
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    s.info.Name,
			"version": s.info.Version,
		},
		"instructions": s.info.Instructions,
	}
}

func (s *Server) callTool(ctx context.Context, raw json.RawMessage) (toolCallResult, *rpcError) {
	var params toolCallParams
	if len(raw) == 0 || json.Unmarshal(raw, &params) != nil || params.Name == "" {
		return toolCallResult{}, &rpcError{Code: codeInvalidParams, Message: "tools/call requires a tool name"}
	}

	output, err := s.runTool(ctx, params.Name, params.Arguments)
	if err != nil && strings.TrimSpace(output) == "" {
		output = err.Error()
	}
	return toolCallResult{
		Content: []textContent{{Type: "text", Text: output}},
		IsError: err != nil,
	}, nil
}

func (s *Server) runTool(ctx context.Context, name string, raw map[string]json.RawMessage) (string, error) {
	tool, ok := s.index[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	arguments, err := decodeArguments(tool.InputSchema, raw)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	return tool.Handler(ctx, arguments)
}
