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

	expected := []string{"repository", "review", "jenkins", "ci", "jira", "confluence", "sonar"}
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

func TestSelectGitLabCommitUsesMergeRequestHeadSHA(t *testing.T) {
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

	selection, err := selectGitLabCommit(context.Background(), git.Context{
		Project: "acme/example", Branch: "feature/test", Commit: "local-head",
	}, values)
	if err != nil {
		t.Fatal(err)
	}
	buildURL, _, err := selection.client.CommitStatus(context.Background(), selection.project, selection.commit, "build")
	if err != nil {
		t.Fatal(err)
	}
	if buildURL != "https://jenkins.example/job/invoice/42/" {
		t.Fatalf("build URL = %q", buildURL)
	}
	expected := map[string]any{"commit": "mr-head", "mr": float64(7)}
	if actual := selection.Map(); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("selection = %#v, want %#v", actual, expected)
	}
}

func TestSelectGitLabCommitFallsBackToCheckoutWithoutMergeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode([]any{})
	}))
	defer server.Close()
	values := config.Values{"GITLAB_API_URL": server.URL, "GITLAB_TOKEN": "token"}
	selection, err := selectGitLabCommit(context.Background(), git.Context{
		Project: "acme/example", Branch: "feature/test", Commit: "local-head",
	}, values)
	if err != nil {
		t.Fatal(err)
	}
	if actual := selection.Map(); !reflect.DeepEqual(actual, map[string]any{"commit": "local-head"}) {
		t.Fatalf("selection = %#v", actual)
	}
}

func TestOpenMergeRequestIidReturnsNilWithoutGitLabConfig(t *testing.T) {
	mergeRequestIid, err := openMergeRequestIid(context.Background(), git.Context{Branch: "feature/test"}, config.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if mergeRequestIid != nil {
		t.Fatalf("openMergeRequestIid() = %v, want nil without GitLab configuration", mergeRequestIid)
	}
}

func TestOpenMergeRequestIidReturnsTheOpenMergeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode([]any{map[string]any{"iid": float64(7), "state": "opened"}})
	}))
	defer server.Close()
	values := config.Values{"GITLAB_API_URL": server.URL, "GITLAB_TOKEN": "token"}

	mergeRequestIid, err := openMergeRequestIid(context.Background(), git.Context{Project: "acme/example", Branch: "feature/test"}, values)
	if err != nil {
		t.Fatal(err)
	}
	if mergeRequestIid != float64(7) {
		t.Fatalf("openMergeRequestIid() = %v, want 7", mergeRequestIid)
	}
}

func TestSonarIssuesUsesPullRequestModeForOpenMergeRequest(t *testing.T) {
	root := testRepository(t, "feature/test")
	repo := git.Open(root)

	sonarServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Query().Get("branch") != "" {
			t.Fatalf("unexpected branch query in pull-request mode: %s", request.URL.RawQuery)
		}
		switch request.URL.Path {
		case "/api/project_pull_requests/list":
			write(writer, map[string]any{"pullRequests": []any{map[string]any{"key": "7"}}})
		case "/api/qualitygates/project_status":
			write(writer, map[string]any{"projectStatus": map[string]any{"conditions": []any{}}})
		case "/api/issues/search":
			write(writer, map[string]any{"issues": []any{}, "paging": map[string]any{"total": 0}})
		default:
			http.Error(writer, "unexpected endpoint: "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer sonarServer.Close()
	gitLabServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode([]any{map[string]any{"iid": float64(7), "state": "opened"}})
	}))
	defer gitLabServer.Close()
	values := config.Values{
		"SONAR_HOST_URL":    sonarServer.URL,
		"SONAR_TOKEN":       "token",
		"SONAR_PROJECT_KEY": "acme:example",
		"GITLAB_API_URL":    gitLabServer.URL,
		"GITLAB_TOKEN":      "token",
	}

	result, err := sonarIssues(context.Background(), repo, values, "")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["mr"] != float64(7) {
		t.Fatalf("sonarIssues() = %#v, want mr 7", decoded)
	}
	if _, ok := decoded["branch"]; ok {
		t.Fatalf("sonarIssues() = %#v, want no branch key in pull-request mode", decoded)
	}
}

func TestSonarIssuesFallsBackToLocalBranchWithoutOpenMergeRequest(t *testing.T) {
	root := testRepository(t, "feature/test")
	repo := git.Open(root)

	sonarServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Query().Get("pullRequest") != "" {
			t.Fatalf("unexpected pullRequest query in branch mode: %s", request.URL.RawQuery)
		}
		switch request.URL.Path {
		case "/api/project_branches/list":
			write(writer, map[string]any{"branches": []any{map[string]any{"name": "feature/test"}}})
		case "/api/qualitygates/project_status":
			write(writer, map[string]any{"projectStatus": map[string]any{"conditions": []any{}}})
		case "/api/issues/search":
			write(writer, map[string]any{"issues": []any{}, "paging": map[string]any{"total": 0}})
		default:
			http.Error(writer, "unexpected endpoint: "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer sonarServer.Close()
	values := config.Values{
		"SONAR_HOST_URL":    sonarServer.URL,
		"SONAR_TOKEN":       "token",
		"SONAR_PROJECT_KEY": "acme:example",
	}

	result, err := sonarIssues(context.Background(), repo, values, "")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["branch"] != "feature/test" {
		t.Fatalf("sonarIssues() = %#v, want branch feature/test", decoded)
	}
	if _, ok := decoded["mr"]; ok {
		t.Fatalf("sonarIssues() = %#v, want no mr key in branch mode", decoded)
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

	projectKey, source, err := sonarProjectKeyFromGitLab(context.Background(), git.Context{
		Project: "acme/example", Branch: "feature/test", Commit: "local-head",
	}, values)
	if err != nil {
		t.Fatal(err)
	}
	if projectKey != "acme:invoice" || source != "gitlab_commit_status" {
		t.Fatalf("project key = %q from %q", projectKey, source)
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

	_, _, err := sonarProjectKeyFromGitLab(context.Background(), git.Context{
		Project: "acme/example", Branch: "feature/test", Commit: "local-head",
	}, values)
	if err == nil || !strings.Contains(err.Error(), "multiple SonarQube project keys") {
		t.Fatalf("sonarProjectKeyFromGitLab() error = %v", err)
	}
}

func TestSonarProjectKeyFromGitLabIgnoresUnrelatedSonarLinks(t *testing.T) {
	sonarServer := httptest.NewServer(http.NotFoundHandler())
	defer sonarServer.Close()
	gitLabServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/merge_requests"):
			_ = json.NewEncoder(writer).Encode([]any{})
		case strings.HasSuffix(request.URL.Path, "/statuses"):
			_ = json.NewEncoder(writer).Encode([]any{map[string]any{
				"name": "documentation", "target_url": sonarServer.URL + "/dashboard?id=acme%3Awiki",
			}})
		default:
			http.Error(writer, "unexpected request: "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer gitLabServer.Close()
	values := config.Values{"SONAR_HOST_URL": sonarServer.URL, "GITLAB_API_URL": gitLabServer.URL, "GITLAB_TOKEN": "token"}

	_, _, err := sonarProjectKeyFromGitLab(context.Background(), git.Context{Project: "acme/example", Branch: "feature/test", Commit: "local-head"}, values)
	if err == nil || !strings.Contains(err.Error(), "no SonarQube project key") {
		t.Fatalf("sonarProjectKeyFromGitLab() error = %v", err)
	}
}

func TestGateRunNormalizesGitLabCommitStatus(t *testing.T) {
	run := gateRun(map[string]any{
		"name": "build", "sha": "abc123", "status": "failed", "target_url": "https://jenkins.example/42",
		"pipeline_id": float64(42), "author": "jenkins", "created_at": "2026-08-11T10:05:00Z",
	})
	expected := map[string]any{
		"gate": "build", "state": "failed", "url": "https://jenkins.example/42",
		"pipeline_id": float64(42), "author": "jenkins", "created_at": "2026-08-11T10:05:00Z",
	}
	if !reflect.DeepEqual(run, expected) {
		t.Fatalf("gateRun() = %#v, want %#v", run, expected)
	}
}

func write(writer http.ResponseWriter, value any) {
	_ = json.NewEncoder(writer).Encode(value)
}

func testRepository(t *testing.T, branch string) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "--initial-branch", branch)
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "initial")
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}
