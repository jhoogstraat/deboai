// Package tools wires the development tool clients into the MCP tools exposed
// by the server.
package tools

import (
	"context"
	"fmt"
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
		values, err := config.Load(repo.Root())
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
// current commit published as a GitLab commit status.
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
		result["merge_request"] = nil
		result["merge_request_lookup"] = nil
	}

	report, err := client.BuildReport(ctx, buildURL)
	if err != nil {
		return "", err
	}
	return jsonutil.Compact(jsonutil.Merge(result, report))
}

// resolveBuildFromGitLab finds the build URL for the checked out commit and
// records the GitLab context it found along the way.
func resolveBuildFromGitLab(ctx context.Context, repoContext git.Context, values config.Values, result map[string]any) (string, error) {
	client, err := gitlab.FromValues(values)
	if err != nil {
		return "", err
	}
	project := values.ValueOr(repoContext.Project, "GITLAB_PROJECT_ID")
	if project == "" {
		return "", fmt.Errorf("no GitLab project found for the origin remote; set GITLAB_PROJECT_ID")
	}
	buildURL, status, err := client.CommitStatus(ctx, project, repoContext.Commit, jenkins.BuildStatusName(values))
	if err != nil {
		return "", err
	}
	mergeRequest, lookup, err := client.MergeRequestLookup(ctx, repoContext)
	if err != nil {
		return "", err
	}
	result["branch"] = jsonutil.Nullable(repoContext.Branch)
	result["commit"] = jsonutil.Nullable(repoContext.Commit)
	result["merge_request"] = mergeRequest
	result["merge_request_lookup"] = lookup
	result["gitlabStatus"] = status
	return buildURL, nil
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
	client, err := sonar.FromValues(values)
	if err != nil {
		return "", err
	}
	issues, err := client.Issues(ctx, branch)
	if err != nil {
		return "", err
	}
	return jsonutil.Compact(issues)
}
