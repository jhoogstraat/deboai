// Package tools wires the development tool clients into the MCP tools exposed
// by the server.
package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/gitlab"
	"github.com/jhoogstraat/deboai/internal/jenkins"
	"github.com/jhoogstraat/deboai/internal/jira"
	"github.com/jhoogstraat/deboai/internal/jsonutil"
	"github.com/jhoogstraat/deboai/internal/mcp"
	"github.com/jhoogstraat/deboai/internal/sonar"
)

// All returns every tool. The worktree is resolved for each call so callers
// can switch worktrees without changing server state.
func All() []mcp.Tool {
	return []mcp.Tool{
		{
			Name:        "repository_context",
			Description: "Return the selected local Git worktree and checkout context.",
			InputSchema: toolSchema(nil),
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, _ config.Values, _ mcp.Arguments) (string, error) {
				return repositoryContext(ctx, repo)
			}),
		},
		{
			Name:        "code_review_context",
			Description: "Return the selected worktree's matching GitLab merge request and latest actionable review comment, when available.",
			InputSchema: toolSchema(nil),
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, _ mcp.Arguments) (string, error) {
				return gitLabMergeRequestContext(ctx, repo, values)
			}),
		},
		{
			Name:        "jenkins_status",
			Description: "Return Jenkins build status, removed-report state, and actionable stage or test failures.",
			InputSchema: toolSchema(map[string]any{
				"build_url": mcp.StringProperty("Optional Jenkins build URL. Omit it to inspect the active commit."),
			}),
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, arguments mcp.Arguments) (string, error) {
				return jenkinsStatus(ctx, repo, values, arguments.String("build_url"))
			}),
		},
		{
			Name:        "ci_gate_runs",
			Description: "Return the CI gate runs published by GitLab for the selected merge request head.",
			InputSchema: toolSchema(nil),
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, _ mcp.Arguments) (string, error) {
				return ciGateRuns(ctx, repo, values)
			}),
		},
		{
			Name:        "jira_ticket",
			Description: "Return compact Jira issue context and download image attachments.",
			InputSchema: toolSchema(map[string]any{
				"ticket": mcp.StringProperty("Jira issue key, for example ABC-123."),
			}, "ticket"),
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, arguments mcp.Arguments) (string, error) {
				return jiraTicket(ctx, repo, values, arguments.String("ticket"))
			}),
		},
		{
			Name:        "sonar_issues",
			Description: "Return failed quality-gate conditions, actionable new-code coverage lines, and confirmed/open SonarQube issues.",
			InputSchema: toolSchema(map[string]any{
				"branch": mcp.StringProperty("Optional Git branch name. Omit it to use the current branch."),
			}),
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, arguments mcp.Arguments) (string, error) {
				return sonarIssues(ctx, repo, values, arguments.String("branch"))
			}),
		},
	}
}

func toolSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	properties["worktree_path"] = mcp.StringProperty("Optional path to a Git worktree. Omit it to use DEBOAI_REPOSITORY_ROOT or the current working directory.")
	return mcp.ObjectSchema(properties, required...)
}

func withWorktree(handler func(context.Context, *git.Repo, config.Values, mcp.Arguments) (string, error)) mcp.Handler {
	return func(ctx context.Context, arguments mcp.Arguments) (string, error) {
		repo, err := git.OpenWorktree(ctx, arguments.String("worktree_path"))
		if err != nil {
			return "", err
		}
		roots := make([]string, 0, 3)
		if strings.TrimSpace(arguments.String("worktree_path")) != "" {
			roots = append(roots, repo.Root())
		}
		if configured := config.Value(git.RootVariable); configured != "" {
			roots = append(roots, configured)
		}
		workingDirectory, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		values, err := config.Load(append(roots, workingDirectory)...)
		if err != nil {
			return "", err
		}
		return handler(ctx, repo, values, arguments)
	}
}

func repositoryContext(ctx context.Context, repo *git.Repo) (string, error) {
	repoContext, err := repo.Context(ctx)
	if err != nil {
		return "", err
	}
	return jsonutil.Compact(repoContext.Map())
}

func gitLabMergeRequestContext(ctx context.Context, repo *git.Repo, values config.Values) (string, error) {
	repoContext, err := repo.Context(ctx)
	if err != nil {
		return "", err
	}
	client, err := gitlab.FromValues(values)
	if err != nil {
		return "", err
	}
	result, err := client.ReviewContext(ctx, repoContext)
	if err != nil {
		return "", err
	}
	return jsonutil.Compact(result)
}

// jenkinsStatus reports on an explicit build URL, or on the build that the
// selected merge request head (falling back to the current commit) published
// as a GitLab commit status.
func jenkinsStatus(ctx context.Context, repo *git.Repo, values config.Values, buildURL string) (string, error) {
	client, err := jenkins.FromValues(values)
	if err != nil {
		return "", err
	}
	repoContext, err := repo.Context(ctx)
	if err != nil {
		return "", err
	}

	result := map[string]any{"repository": repoContext.Map()}
	buildURL = strings.TrimSpace(buildURL)
	if buildURL == "" {
		if buildURL, err = resolveBuildFromGitLab(ctx, repoContext, values, result); err != nil {
			return "", err
		}
	} else {
		// An explicit build URL need not belong to the checked out commit,
		// so no branch, commit, or merge request context is reported.
		result["branch"] = nil
		result["commit"] = nil
		result["checkout_commit"] = nil
		result["merge_request"] = nil
		result["merge_request_lookup"] = nil
	}

	report, err := client.BuildReport(ctx, buildURL)
	if err != nil {
		return "", err
	}
	return jsonutil.Compact(jsonutil.Merge(result, report))
}

// resolveBuildFromGitLab finds the build URL for the selected merge request
// head, falling back to the checked out commit, and records its provenance.
func resolveBuildFromGitLab(ctx context.Context, repoContext git.Context, values config.Values, result map[string]any) (string, error) {
	client, err := gitlab.FromValues(values)
	if err != nil {
		return "", err
	}
	project := values.ValueOr(repoContext.Project, "GITLAB_PROJECT_ID")
	if project == "" {
		return "", fmt.Errorf("no GitLab project found for the origin remote; set GITLAB_PROJECT_ID")
	}
	commit, mergeRequest, lookup, err := mergeRequestCommit(ctx, client, repoContext)
	if err != nil {
		return "", err
	}
	if commit == "" {
		return "", fmt.Errorf("no commit found for the selected GitLab merge request or worktree")
	}
	buildURL, status, err := client.CommitStatus(ctx, project, commit, jenkins.BuildStatusName(values))
	if err != nil {
		return "", err
	}
	result["branch"] = jsonutil.Nullable(repoContext.Branch)
	result["commit"] = jsonutil.Nullable(commit)
	result["checkout_commit"] = jsonutil.Nullable(repoContext.Commit)
	result["merge_request"] = mergeRequest
	result["merge_request_lookup"] = lookup
	result["gitlabStatus"] = status
	return buildURL, nil
}

// mergeRequestCommit returns the selected MR's current head SHA. A local
// checkout is allowed to be stale, so it is only used when GitLab has no
// selected MR for the branch.
func mergeRequestCommit(ctx context.Context, client *gitlab.Client, repoContext git.Context) (string, any, map[string]any, error) {
	mergeRequest, lookup, err := client.MergeRequestLookup(ctx, repoContext)
	if err != nil {
		return "", nil, nil, err
	}
	if sha := jsonutil.String(jsonutil.Map(mergeRequest)["sha"]); sha != "" {
		return sha, mergeRequest, lookup, nil
	}
	return repoContext.Commit, mergeRequest, lookup, nil
}

// ciGateRuns exposes the structured GitLab records that bridge an MR commit to
// its external CI systems. It intentionally does not scrape MR comments.
func ciGateRuns(ctx context.Context, repo *git.Repo, values config.Values) (string, error) {
	repoContext, err := repo.Context(ctx)
	if err != nil {
		return "", err
	}
	client, err := gitlab.FromValues(values)
	if err != nil {
		return "", err
	}
	project := values.ValueOr(repoContext.Project, "GITLAB_PROJECT_ID")
	if project == "" {
		return "", fmt.Errorf("no GitLab project found for the origin remote; set GITLAB_PROJECT_ID")
	}
	commit, mergeRequest, lookup, err := mergeRequestCommit(ctx, client, repoContext)
	if err != nil {
		return "", err
	}
	if commit == "" {
		return "", fmt.Errorf("no commit found for the selected GitLab merge request or worktree")
	}
	statuses, err := client.CommitStatuses(ctx, project, commit)
	if err != nil {
		return "", err
	}
	gates := make([]any, 0, len(statuses))
	for _, status := range statuses {
		gates = append(gates, gateRun(status))
	}
	return jsonutil.Compact(map[string]any{
		"repository":           repoContext.Map(),
		"branch":               jsonutil.Nullable(repoContext.Branch),
		"commit":               commit,
		"checkout_commit":      jsonutil.Nullable(repoContext.Commit),
		"merge_request":        mergeRequest,
		"merge_request_lookup": lookup,
		"gates":                gates,
	})
}

func gateRun(status map[string]any) map[string]any {
	run := map[string]any{
		"source":     "gitlab_commit_status",
		"gate":       status["name"],
		"commit_sha": status["sha"],
		"state":      status["status"],
		"url":        status["target_url"],
	}
	for _, key := range []string{"id", "description", "pipeline_id", "author", "created_at", "started_at", "finished_at", "ref"} {
		if status[key] != nil {
			run[key] = status[key]
		}
	}
	return run
}

func jiraTicket(ctx context.Context, repo *git.Repo, values config.Values, ticket string) (string, error) {
	client, err := jira.FromValues(values)
	if err != nil {
		return "", err
	}
	issue, err := client.Issue(ctx, ticket, repo.Root())
	if err != nil {
		return "", err
	}
	return jsonutil.Compact(issue)
}

func sonarIssues(ctx context.Context, repo *git.Repo, values config.Values, branch string) (string, error) {
	if strings.TrimSpace(branch) == "" {
		current, err := repo.CurrentBranch(ctx)
		if err != nil {
			return "", err
		}
		if branch = strings.TrimSpace(current); branch == "" {
			return "", fmt.Errorf("no active Git branch; pass a SonarQube branch name")
		}
	}
	projectKey, source, gitLabStatus, err := sonarProjectKey(ctx, repo, values)
	if err != nil {
		return "", err
	}
	client, err := sonar.FromValuesWithProjectKey(values, projectKey)
	if err != nil {
		return "", err
	}
	issues, err := client.Issues(ctx, branch)
	if err != nil {
		return "", err
	}
	issues["projectKey"] = projectKey
	issues["projectKeySource"] = source
	if gitLabStatus != nil {
		issues["gitlabStatus"] = gitLabStatus
	}
	return jsonutil.Compact(issues)
}

// sonarProjectKey uses explicit configuration first. When it is absent, it
// accepts only a Sonar URL on a GitLab status for the selected MR head SHA.
func sonarProjectKey(ctx context.Context, repo *git.Repo, values config.Values) (string, string, map[string]any, error) {
	if projectKey := values.Value("SONAR_PROJECT_KEY"); projectKey != "" {
		return projectKey, "environment", nil, nil
	}
	repoContext, err := repo.Context(ctx)
	if err != nil {
		return "", "", nil, err
	}
	return sonarProjectKeyFromGitLab(ctx, repoContext, values)
}

func sonarProjectKeyFromGitLab(ctx context.Context, repoContext git.Context, values config.Values) (string, string, map[string]any, error) {
	baseURL, err := sonar.BaseURLFromValues(values)
	if err != nil {
		return "", "", nil, err
	}
	gitLabClient, err := gitlab.FromValues(values)
	if err != nil {
		return "", "", nil, err
	}
	project := values.ValueOr(repoContext.Project, "GITLAB_PROJECT_ID")
	if project == "" {
		return "", "", nil, fmt.Errorf("no GitLab project found for the origin remote; set GITLAB_PROJECT_ID")
	}
	commit, _, _, err := mergeRequestCommit(ctx, gitLabClient, repoContext)
	if err != nil {
		return "", "", nil, err
	}
	if commit == "" {
		return "", "", nil, fmt.Errorf("no commit found for the selected GitLab merge request or worktree")
	}
	statuses, err := gitLabClient.CommitStatuses(ctx, project, commit)
	if err != nil {
		return "", "", nil, err
	}
	candidates := map[string]map[string]any{}
	for _, status := range statuses {
		if projectKey, ok := sonar.ProjectKeyFromURL(baseURL, jsonutil.String(status["target_url"])); ok {
			candidates[projectKey] = status
		}
	}
	if len(candidates) == 1 {
		for projectKey, status := range candidates {
			return projectKey, "gitlab_commit_status", status, nil
		}
	}
	if len(candidates) > 1 {
		return "", "", nil, fmt.Errorf("multiple SonarQube project keys found in GitLab statuses for commit %s; set SONAR_PROJECT_KEY", commit)
	}
	return "", "", nil, fmt.Errorf("no SonarQube project key found in GitLab statuses for commit %s; set SONAR_PROJECT_KEY", commit)
}
