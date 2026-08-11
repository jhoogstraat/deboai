package tools

import (
	"reflect"
	"testing"
)

func TestAllExposesTheDocumentedTools(t *testing.T) {
	tools := All()

	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.Handler == nil {
			t.Fatalf("tool %q is missing a description or handler", tool.Name)
		}
		if tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("tool %q accepts undeclared arguments", tool.Name)
		}
		properties, _ := tool.InputSchema["properties"].(map[string]any)
		if properties["worktree_path"] == nil {
			t.Fatalf("tool %q does not accept a worktree path", tool.Name)
		}
	}

	expected := []string{"repository_context", "code_review_context", "jenkins_status", "jira_ticket", "sonar_issues"}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("All() = %#v, want %#v", names, expected)
	}
}
