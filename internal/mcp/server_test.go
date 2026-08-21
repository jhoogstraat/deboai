package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jhoogstraat/deboai/internal/tools"
)

func testServer() *Server {
	return NewServer(
		Info{Name: "test", Version: "0.0.0"},
		Tool{
			Name:        "echo",
			InputSchema: ObjectSchema(map[string]any{"ticket": StringProperty("issue key")}, "ticket"),
			Handler: func(_ context.Context, arguments tools.Arguments) (string, error) {
				return arguments.String("ticket"), nil
			},
		},
	)
}

func TestChooseProtocolVersion(t *testing.T) {
	for _, test := range []struct {
		requested string
		expected  string
	}{
		{requested: "2025-03-26", expected: "2025-03-26"},
		{requested: "2025-11-25", expected: "2025-11-25"},
		{requested: "2026-07-28", expected: "2026-07-28"},
		{requested: "2026-02-01", expected: "2025-11-25"},
		{requested: "2027-01-01", expected: CurrentProtocol},
		{requested: "2024-10-07", expected: "2024-11-05"},
		{requested: "unsupported", expected: CurrentProtocol},
		{requested: "", expected: CurrentProtocol},
	} {
		if actual := chooseProtocolVersion(test.requested); actual != test.expected {
			t.Fatalf("chooseProtocolVersion(%q) = %q, want %q", test.requested, actual, test.expected)
		}
	}
}

func TestDecodeArgumentsRejectsInvalidValues(t *testing.T) {
	schema := ObjectSchema(map[string]any{"ticket": StringProperty("issue key")}, "ticket")
	for name, raw := range map[string]map[string]json.RawMessage{
		"missing":   {},
		"empty":     {"ticket": json.RawMessage(`""`)},
		"blank":     {"ticket": json.RawMessage(`"  "`)},
		"nonstring": {"ticket": json.RawMessage(`7`)},
		"unknown":   {"ticket": json.RawMessage(`"ABC-1"`), "other": json.RawMessage(`"x"`)},
	} {
		if _, err := decodeArguments(schema, raw); err == nil {
			t.Fatalf("decodeArguments accepted %s arguments", name)
		}
	}

	arguments, err := decodeArguments(schema, map[string]json.RawMessage{"ticket": json.RawMessage(`"ABC-1"`)})
	if err != nil {
		t.Fatal(err)
	}
	if arguments.String("ticket") != "ABC-1" {
		t.Fatalf("decodeArguments() = %#v, want ticket ABC-1", arguments)
	}
}

func TestDecodeArgumentsAllowsOmittedOptionalArguments(t *testing.T) {
	schema := ObjectSchema(map[string]any{"branch": StringProperty("branch")})
	arguments, err := decodeArguments(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	if arguments.String("branch") != "" {
		t.Fatalf("decodeArguments() = %#v, want an empty branch", arguments)
	}
}

// serve runs one request through the server and returns the decoded response.
func serve(t *testing.T, request string) map[string]any {
	t.Helper()
	output := &strings.Builder{}
	if err := testServer().Serve(context.Background(), strings.NewReader(request+"\n"), output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(output.String()), &response); err != nil {
		t.Fatalf("decode response %q: %v", output.String(), err)
	}
	return response
}

func TestServeInitializeReportsServerInfo(t *testing.T) {
	response := serve(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	result, _ := response["result"].(map[string]any)
	if result["protocolVersion"] != "2024-11-05" {
		t.Fatalf("initialize protocolVersion = %v, want 2024-11-05", result["protocolVersion"])
	}
	serverInfo, _ := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "test" {
		t.Fatalf("initialize serverInfo = %#v, want name test", serverInfo)
	}
}

func TestServeInitializeDefaultsToCurrentProtocol(t *testing.T) {
	response := serve(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	result, _ := response["result"].(map[string]any)
	if result["protocolVersion"] != CurrentProtocol {
		t.Fatalf("initialize protocolVersion = %v, want %v", result["protocolVersion"], CurrentProtocol)
	}
}

func TestServeToolsListOmitsHandlers(t *testing.T) {
	response := serve(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result, _ := response["result"].(map[string]any)
	list, _ := result["tools"].([]any)
	if len(list) != 1 {
		t.Fatalf("tools/list returned %d tools, want 1", len(list))
	}
	tool, _ := list[0].(map[string]any)
	if tool["name"] != "echo" {
		t.Fatalf("tools/list = %#v, want the echo tool", tool)
	}
	if _, ok := tool["Handler"]; ok {
		t.Fatalf("tools/list exposed the handler: %#v", tool)
	}
}

func TestServeUnknownToolReportsToolError(t *testing.T) {
	response := serve(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"missing"}}`)
	result, _ := response["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("tools/call for an unknown tool = %#v, want isError", response)
	}
}

func TestServeRejectsMalformedMessages(t *testing.T) {
	response := serve(t, `{"not":"json-rpc"}`)
	rpcErr, _ := response["error"].(map[string]any)
	if rpcErr == nil {
		t.Fatalf("malformed message response = %#v, want an error", response)
	}
}

func TestServeIgnoresNotifications(t *testing.T) {
	output := &strings.Builder{}
	request := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	if err := testServer().Serve(context.Background(), strings.NewReader(request), output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "" {
		t.Fatalf("notification produced a response: %q", output.String())
	}
}
