package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/mcp"
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

func TestWithWorktreeLoadsEnvDirectoriesConditionally(t *testing.T) {
	worktree := t.TempDir()
	repositoryRoot := t.TempDir()
	workingDirectory := t.TempDir()
	for _, root := range []string{worktree, repositoryRoot} {
		command := exec.Command("git", "init", "--quiet")
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, output)
		}
	}
	for path, contents := range map[string]string{
		filepath.Join(worktree, ".env"):         "DEBOAI_TEST_SELECTED=worktree\n",
		filepath.Join(repositoryRoot, ".env"):   "DEBOAI_TEST_SELECTED=repository\nDEBOAI_TEST_REPOSITORY=repository\n",
		filepath.Join(workingDirectory, ".env"): "DEBOAI_TEST_SELECTED=working\nDEBOAI_TEST_REPOSITORY=working\nDEBOAI_TEST_WORKING=working\n",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(git.RootVariable, repositoryRoot)
	t.Setenv(config.EnvFileVariable, "")
	t.Chdir(workingDirectory)

	handler := withWorktree(func(_ context.Context, _ *git.Repo, values config.Values, _ mcp.Arguments) (string, error) {
		return values.Value("DEBOAI_TEST_SELECTED") + "," + values.Value("DEBOAI_TEST_REPOSITORY") + "," + values.Value("DEBOAI_TEST_WORKING"), nil
	})
	result, err := handler(context.Background(), mcp.Arguments{"worktree_path": worktree})
	if err != nil {
		t.Fatal(err)
	}
	if result != "worktree,repository,working" {
		t.Fatalf("withWorktree() = %q, want worktree,repository,working", result)
	}

	result, err = handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "repository,repository,working" {
		t.Fatalf("withWorktree() without path = %q, want repository,repository,working", result)
	}
}
