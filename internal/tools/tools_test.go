package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/gitlab"
)

func TestAllExposesTheDocumentedTools(t *testing.T) {
	tools := All()

	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.Handler == nil {
			t.Fatalf("tool %q is missing a description or handler", tool.Name)
		}
		if !hasArgument(tool.Arguments, "worktree_path") {
			t.Fatalf("tool %q does not accept a worktree path", tool.Name)
		}
	}

	expected := []string{"repository_context", "code_review_context", "jenkins_status", "ci_gate_runs", "jira_ticket", "sonar_issues"}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("All() = %#v, want %#v", names, expected)
	}
}

func hasArgument(arguments []Argument, name string) bool {
	for _, argument := range arguments {
		if argument.Name == name {
			return true
		}
	}
	return false
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

	handler := withWorktree(func(_ context.Context, _ *git.Repo, values config.Values, _ Arguments) (string, error) {
		return values.Value("DEBOAI_TEST_SELECTED") + "," + values.Value("DEBOAI_TEST_REPOSITORY") + "," + values.Value("DEBOAI_TEST_WORKING"), nil
	})
	result, err := handler(context.Background(), Arguments{"worktree_path": worktree})
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

func TestResolveBuildFromGitLabUsesMergeRequestHeadSHA(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/merge_requests"):
			_ = json.NewEncoder(writer).Encode([]any{map[string]any{
				"iid": 7, "state": "opened", "sha": "mr-head", "updated_at": "2026-08-11T10:00:00Z",
			}})
		case strings.Contains(request.URL.Path, "/repository/commits/mr-head/statuses"):
			_ = json.NewEncoder(writer).Encode([]any{map[string]any{
				"name": "build", "status": "failed", "target_url": "https://jenkins.example/job/invoice/42/", "created_at": "2026-08-11T10:05:00Z",
			}})
		case strings.Contains(request.URL.Path, "/repository/commits/local-head/statuses"):
			http.Error(writer, "used stale local checkout", http.StatusInternalServerError)
		default:
			http.Error(writer, "unexpected request: "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	values := config.Values{"GITLAB_API_URL": server.URL, "GITLAB_TOKEN": "token"}

	result := map[string]any{}
	buildURL, err := resolveBuildFromGitLab(context.Background(), git.Context{
		Project: "acme/example", Branch: "feature/test", Commit: "local-head",
	}, values, result)
	if err != nil {
		t.Fatal(err)
	}
	if buildURL != "https://jenkins.example/job/invoice/42/" {
		t.Fatalf("build URL = %q", buildURL)
	}
	if result["commit"] != "mr-head" || result["checkout_commit"] != "local-head" {
		t.Fatalf("commit provenance = %#v", result)
	}
	status, _ := result["gitlabStatus"].(map[string]any)
	if status["sha"] != "mr-head" || status["name"] != "build" {
		t.Fatalf("gitlabStatus = %#v", status)
	}
}

func TestMergeRequestCommitFallsBackToCheckoutWithoutMergeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode([]any{})
	}))
	defer server.Close()
	client, err := gitlab.New(gitlab.Options{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	commit, mergeRequest, lookup, err := mergeRequestCommit(context.Background(), client, git.Context{
		Project: "acme/example", Branch: "feature/test", Commit: "local-head",
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit != "local-head" || mergeRequest != nil || lookup["reason"] != "no_matching_merge_request" {
		t.Fatalf("mergeRequestCommit() = %q, %#v, %#v", commit, mergeRequest, lookup)
	}
}

func TestSonarProjectKeyFromGitLabUsesMergeRequestHeadStatus(t *testing.T) {
	sonarServer := httptest.NewServer(http.NotFoundHandler())
	defer sonarServer.Close()
	gitLabServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/merge_requests"):
			_ = json.NewEncoder(writer).Encode([]any{map[string]any{
				"iid": 7, "state": "opened", "sha": "mr-head", "updated_at": "2026-08-11T10:00:00Z",
			}})
		case strings.Contains(request.URL.Path, "/repository/commits/mr-head/statuses"):
			_ = json.NewEncoder(writer).Encode([]any{map[string]any{
				"name": "SonarQube", "status": "failed", "target_url": sonarServer.URL + "/dashboard?id=acme%3Ainvoice&pullRequest=7", "created_at": "2026-08-11T10:05:00Z",
			}})
		case strings.Contains(request.URL.Path, "/repository/commits/local-head/statuses"):
			http.Error(writer, "used stale local checkout", http.StatusInternalServerError)
		default:
			http.Error(writer, "unexpected request: "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer gitLabServer.Close()
	values := config.Values{
		"SONAR_HOST_URL": sonarServer.URL,
		"GITLAB_API_URL": gitLabServer.URL,
		"GITLAB_TOKEN":   "token",
	}

	projectKey, source, status, err := sonarProjectKeyFromGitLab(context.Background(), git.Context{
		Project: "acme/example", Branch: "feature/test", Commit: "local-head",
	}, values)
	if err != nil {
		t.Fatal(err)
	}
	if projectKey != "acme:invoice" || source != "gitlab_commit_status" {
		t.Fatalf("project key = %q from %q", projectKey, source)
	}
	if status["sha"] != "mr-head" || status["name"] != "SonarQube" {
		t.Fatalf("gitlab status = %#v", status)
	}
}

func TestSonarProjectKeyFromGitLabRejectsAmbiguousStatuses(t *testing.T) {
	sonarServer := httptest.NewServer(http.NotFoundHandler())
	defer sonarServer.Close()
	gitLabServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/merge_requests"):
			_ = json.NewEncoder(writer).Encode([]any{})
		case strings.HasSuffix(request.URL.Path, "/statuses"):
			_ = json.NewEncoder(writer).Encode([]any{
				map[string]any{"name": "SonarQube", "target_url": sonarServer.URL + "/dashboard?id=acme%3Aone"},
				map[string]any{"name": "SonarQube", "target_url": sonarServer.URL + "/dashboard?id=acme%3Atwo"},
			})
		default:
			http.Error(writer, "unexpected request: "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer gitLabServer.Close()
	values := config.Values{
		"SONAR_HOST_URL": sonarServer.URL,
		"GITLAB_API_URL": gitLabServer.URL,
		"GITLAB_TOKEN":   "token",
	}

	_, _, _, err := sonarProjectKeyFromGitLab(context.Background(), git.Context{
		Project: "acme/example", Branch: "feature/test", Commit: "local-head",
	}, values)
	if err == nil || !strings.Contains(err.Error(), "multiple SonarQube project keys") {
		t.Fatalf("sonarProjectKeyFromGitLab() error = %v", err)
	}
}

func TestGateRunNormalizesGitLabCommitStatus(t *testing.T) {
	run := gateRun(map[string]any{
		"name": "build", "sha": "abc123", "status": "failed", "target_url": "https://jenkins.example/42",
		"pipeline_id": float64(42), "author": "jenkins", "created_at": "2026-08-11T10:05:00Z",
	})
	expected := map[string]any{
		"source": "gitlab_commit_status", "gate": "build", "commit_sha": "abc123", "state": "failed", "url": "https://jenkins.example/42",
		"pipeline_id": float64(42), "author": "jenkins", "created_at": "2026-08-11T10:05:00Z",
	}
	if !reflect.DeepEqual(run, expected) {
		t.Fatalf("gateRun() = %#v, want %#v", run, expected)
	}
}
