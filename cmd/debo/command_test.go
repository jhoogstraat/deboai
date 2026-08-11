package main

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/jhoogstraat/deboai/internal/tools"
)

func TestRootShowsCLIDefaultHelp(t *testing.T) {
	output, _, err := execute(t, nil, strings.NewReader(`{"jsonrpc":"2.0"}`), testDefinitions(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Usage:", "debo [command]", "ci", "jenkins", "jira", "repository", "review", "sonar", "completion", "--mcp"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("root help does not contain %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "jsonrpc") {
		t.Fatalf("root command consumed MCP input:\n%s", output)
	}
}

func TestCommandHelpAndVersion(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		expected  []string
	}{
		{arguments: []string{"--version"}, expected: []string{"debo version test"}},
		{arguments: []string{"jenkins", "--help"}, expected: []string{"debo jenkins [build-url]", "--worktree"}},
		{arguments: []string{"jira", "--help"}, expected: []string{"debo jira <ticket>", "--worktree"}},
		{arguments: []string{"sonar", "--help"}, expected: []string{"debo sonar [branch]", "--worktree"}},
	} {
		output, _, err := execute(t, test.arguments, strings.NewReader(""), testDefinitions(nil))
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range test.expected {
			if !strings.Contains(output, expected) {
				t.Fatalf("%v output does not contain %q:\n%s", test.arguments, expected, output)
			}
		}
	}
}

func TestCommandsMapPositionalArguments(t *testing.T) {
	tests := []struct {
		name     string
		command  []string
		tool     string
		expected tools.Arguments
	}{
		{name: "repository", command: []string{"--worktree", "/repo", "repository"}, tool: "repository_context", expected: tools.Arguments{"worktree_path": "/repo"}},
		{name: "review", command: []string{"review"}, tool: "code_review_context", expected: tools.Arguments{}},
		{name: "ci", command: []string{"ci"}, tool: "ci_gate_runs", expected: tools.Arguments{}},
		{name: "jenkins default", command: []string{"jenkins"}, tool: "jenkins_status", expected: tools.Arguments{}},
		{name: "jenkins URL", command: []string{"jenkins", "https://jenkins.example/job/example/42/"}, tool: "jenkins_status", expected: tools.Arguments{"build_url": "https://jenkins.example/job/example/42/"}},
		{name: "jira", command: []string{"jira", "ABC-123"}, tool: "jira_ticket", expected: tools.Arguments{"ticket": "ABC-123"}},
		{name: "sonar default", command: []string{"sonar"}, tool: "sonar_issues", expected: tools.Arguments{}},
		{name: "sonar branch", command: []string{"sonar", "feature/example"}, tool: "sonar_issues", expected: tools.Arguments{"branch": "feature/example"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := ""
			var actual tools.Arguments
			definitions := testDefinitions(func(name string, arguments tools.Arguments) {
				called = name
				actual = arguments
			})
			output, errorOutput, err := execute(t, test.command, strings.NewReader(""), definitions)
			if err != nil {
				t.Fatal(err)
			}
			if called != test.tool || !reflect.DeepEqual(actual, test.expected) {
				t.Fatalf("called %q with %#v, want %q with %#v", called, actual, test.tool, test.expected)
			}
			if output != "{\"ok\":true}\n" || errorOutput != "" {
				t.Fatalf("stdout = %q, stderr = %q", output, errorOutput)
			}
		})
	}
}

func TestCommandsRejectInvalidArguments(t *testing.T) {
	for name, arguments := range map[string][]string{
		"missing Jira ticket": {"jira"},
		"extra Jenkins URL":   {"jenkins", "one", "two"},
		"removed build flag":  {"jenkins", "--build-url", "url"},
		"removed branch flag": {"sonar", "--branch", "main"},
	} {
		t.Run(name, func(t *testing.T) {
			output, _, err := execute(t, arguments, strings.NewReader(""), testDefinitions(nil))
			if err == nil {
				t.Fatal("invalid arguments succeeded")
			}
			if output != "" {
				t.Fatalf("invalid arguments wrote stdout: %q", output)
			}
		})
	}
}

func TestRuntimeErrorsReturnWithoutWritingOutput(t *testing.T) {
	definitions := testDefinitions(nil)
	definitions[0].Handler = func(context.Context, tools.Arguments) (string, error) {
		return "", errors.New("failed")
	}
	output, errorOutput, err := execute(t, []string{"repository"}, strings.NewReader(""), definitions)
	if err == nil || err.Error() != "failed" {
		t.Fatalf("Execute() error = %v", err)
	}
	if output != "" || errorOutput != "" {
		t.Fatalf("stdout = %q, stderr = %q", output, errorOutput)
	}
}

func TestMCPRequiresDedicatedRootFlag(t *testing.T) {
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
	output, _, err := execute(t, []string{"--mcp"}, strings.NewReader(request), testDefinitions(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"name":"repository_context"`) || strings.Contains(output, "Usage:") {
		t.Fatalf("MCP output = %q", output)
	}

	for name, arguments := range map[string][]string{
		"command":  {"--mcp", "repository"},
		"worktree": {"--mcp", "--worktree", "/repo"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := execute(t, arguments, strings.NewReader(request), testDefinitions(nil)); err == nil {
				t.Fatal("mixed MCP invocation succeeded")
			}
		})
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			output, _, err := execute(t, []string{"completion", shell}, strings.NewReader(""), testDefinitions(nil))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, "debo") {
				t.Fatalf("completion output does not mention debo: %q", output)
			}
		})
	}
}

func execute(t *testing.T, arguments []string, input io.Reader, definitions []tools.Definition) (string, string, error) {
	t.Helper()
	output := &strings.Builder{}
	errorOutput := &strings.Builder{}
	command := newRootCommand("test", definitions, input, output, errorOutput)
	command.SetArgs(arguments)
	err := command.ExecuteContext(context.Background())
	return output.String(), errorOutput.String(), err
}

func testDefinitions(called func(string, tools.Arguments)) []tools.Definition {
	names := []string{"repository_context", "code_review_context", "jenkins_status", "ci_gate_runs", "jira_ticket", "sonar_issues"}
	definitions := make([]tools.Definition, 0, len(names))
	for _, name := range names {
		name := name
		definitions = append(definitions, tools.Definition{
			Name:        name,
			Description: name,
			Handler: func(_ context.Context, arguments tools.Arguments) (string, error) {
				if called != nil {
					called(name, arguments)
				}
				return `{"ok":true}`, nil
			},
		})
	}
	return definitions
}
