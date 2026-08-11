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

// All returns every tool. Repository and environment are resolved per call so
// one server can safely inspect multiple worktrees.
func All(defaultDirectory string) []mcp.Tool {
	return []mcp.Tool{
		{
			Name:        "repository_context",
			Description: "Return the current local Git repository and checkout context.",
			InputSchema: repositorySchema(nil),
			Handler: func(ctx context.Context, arguments mcp.Arguments) (string, error) {
				repo, _, err := repository(defaultDirectory, arguments)
				if err != nil {
					return "", err
				}
				return repositoryContext(ctx, repo)
			},
		},
		{
			Name:        "code_review_context",
			Description: "Return the matching GitLab merge request and its latest actionable review comment, when available.",
			InputSchema: repositorySchema(nil),
			Handler: func(ctx context.Context, arguments mcp.Arguments) (string, error) {
				repo, values, err := repository(defaultDirectory, arguments)
				if err != nil {
					return "", err
				}
				return gitLabMergeRequestContext(ctx, repo, values)
			},
		},
		{
			Name:        "jenkins_status",
			Description: "Return Jenkins build status, removed-report state, and actionable stage or test failures.",
			InputSchema: repositorySchema(map[string]any{
				"build_url": mcp.StringProperty("Optional Jenkins build URL. Omit it to inspect the active commit."),
			}),
			Handler: func(ctx context.Context, arguments mcp.Arguments) (string, error) {
				repo, values, err := repository(defaultDirectory, arguments)
				if err != nil {
					return "", err
				}
				return jenkinsStatus(ctx, repo, values, arguments.String("build_url"))
			},
		},
		{
			Name:        "jira_ticket",
			Description: "Return compact Jira issue context and download image attachments.",
			InputSchema: repositorySchema(map[string]any{
				"ticket": mcp.StringProperty("Jira issue key, for example ABC-123."),
			}, "ticket"),
			Handler: func(ctx context.Context, arguments mcp.Arguments) (string, error) {
				repo, values, err := repository(defaultDirectory, arguments)
				if err != nil {
					return "", err
				}
				return jiraTicket(ctx, repo, values, arguments.String("ticket"))
			},
		},
		{
			Name:        "sonar_issues",
			Description: "Return failed quality-gate conditions, actionable new-code coverage lines, and confirmed/open SonarQube issues.",
			InputSchema: repositorySchema(map[string]any{
				"branch": mcp.StringProperty("Optional Git branch name. Omit it to use the current branch."),
			}),
			Handler: func(ctx context.Context, arguments mcp.Arguments) (string, error) {
				repo, values, err := repository(defaultDirectory, arguments)
				if err != nil {
					return "", err
				}
				return sonarIssues(ctx, repo, values, arguments.String("branch"))
			},
		},
	}
}

func repositorySchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	properties["repository_root"] = mcp.StringProperty("Optional path inside the Git worktree to inspect. Defaults to the server start directory.")
	return mcp.ObjectSchema(properties, required...)
}

func repository(defaultDirectory string, arguments mcp.Arguments) (*git.Repo, config.Values, error) {
	directory := strings.TrimSpace(arguments.String("repository_root"))
	if directory == "" {
		directory = defaultDirectory
	}
	root, err := git.DiscoverRoot(directory)
	if err != nil {
		return nil, nil, err
	}
	values, err := config.Load(root)
	if err != nil {
		return nil, nil, err
	}
	return git.Open(root), values, nil
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
