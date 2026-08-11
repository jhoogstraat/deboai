package tools

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jhoogstraat/deboai/internal/mcp"
)

func TestAllExposesTheDocumentedTools(t *testing.T) {
	tools := All(t.TempDir())

	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.Handler == nil {
			t.Fatalf("tool %q is missing a description or handler", tool.Name)
		}
		if tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("tool %q accepts undeclared arguments", tool.Name)
		}
		properties := tool.InputSchema["properties"].(map[string]any)
		if _, ok := properties["repository_root"]; !ok {
			t.Fatalf("tool %q is missing repository_root", tool.Name)
		}
	}

	expected := []string{"repository_context", "code_review_context", "jenkins_status", "jira_ticket", "sonar_issues"}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("All() = %#v, want %#v", names, expected)
	}
}

func TestRepositoryRootIsResolvedPerCall(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("git", "init", "--quiet")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	command = exec.Command("git", "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--quiet", "--allow-empty", "-m", "initial")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}

	tool := All(t.TempDir())[0]
	output, err := tool.Handler(context.Background(), mcp.Arguments{"repository_root": root})
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"root":"`+resolvedRoot+`"`) || !strings.Contains(output, `"cwdIsRoot":true`) {
		t.Fatalf("repository context %s does not describe %s", output, resolvedRoot)
	}
}
