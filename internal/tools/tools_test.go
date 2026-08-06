package tools

import (
	"reflect"
	"testing"

	"github.com/jhoogstraat/deboai/internal/git"
)

func TestAllExposesTheDocumentedTools(t *testing.T) {
	tools := All(git.Open(t.TempDir()))

	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.Handler == nil {
			t.Fatalf("tool %q is missing a description or handler", tool.Name)
		}
		if tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("tool %q accepts undeclared arguments", tool.Name)
		}
	}

	expected := []string{"gitlab_review_context", "jenkins_status", "jira_ticket", "sonar_issues"}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("All() = %#v, want %#v", names, expected)
	}
}
