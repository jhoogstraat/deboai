// Package tools defines and implements the repository development tools.
package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/confluence"
	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/gitlab"
	"github.com/jhoogstraat/deboai/internal/jenkins"
	"github.com/jhoogstraat/deboai/internal/jira"
	"github.com/jhoogstraat/deboai/internal/jsonutil"
	"github.com/jhoogstraat/deboai/internal/sonar"
)

// Arguments holds the validated string arguments of a tool call.
type Arguments map[string]string

// String returns the argument, or the empty string when it was omitted.
func (a Arguments) String(name string) string {
	return a[name]
}

// Handler runs a tool and returns its compact JSON result.
type Handler func(ctx context.Context, arguments Arguments) (string, error)

// Argument describes one accepted tool argument.
type Argument struct {
	Name        string
	Description string
	Required    bool
}

// Definition describes a callable tool independently of its CLI or MCP adapter.
type Definition struct {
	Name        string
	Description string
	Arguments   []Argument
	Handler     Handler
}

var worktreeArgument = Argument{
	Name:        "worktree_path",
	Description: "Optional path to a Git worktree. Omit it to use DEBOAI_REPOSITORY_ROOT or the current working directory.",
}

// All returns every tool. The worktree is resolved for each call so callers
// can switch worktrees without changing process state.
func All() []Definition {
	return []Definition{
		{
			Name:        "repository",
			Description: "Return the selected local Git worktree and checkout context.",
			Arguments:   []Argument{worktreeArgument},
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, _ config.Values, _ Arguments) (string, error) {
				return repositoryContext(ctx, repo)
			}),
		},
		{
			Name:        "review",
			Description: "Return the selected worktree's matching GitLab merge request and latest actionable review comment, when available.",
			Arguments:   []Argument{worktreeArgument},
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, _ Arguments) (string, error) {
				return gitLabMergeRequestContext(ctx, repo, values)
			}),
		},
		{
			Name:        "jenkins",
			Description: "Return Jenkins build status, removed-report state, and actionable stage or test failures.",
			Arguments: []Argument{
				{Name: "build_url", Description: "Optional Jenkins build URL. Omit it to inspect the active commit."},
				worktreeArgument,
			},
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, arguments Arguments) (string, error) {
				return jenkinsStatus(ctx, repo, values, arguments.String("build_url"))
			}),
		},
		{
			Name:        "ci",
			Description: "Return the CI gate runs published by GitLab for the selected merge request head.",
			Arguments:   []Argument{worktreeArgument},
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, _ Arguments) (string, error) {
				return ciGateRuns(ctx, repo, values)
			}),
		},
		{
			Name:        "jira",
			Description: "Return compact Jira issue context and optionally download one attachment.",
			Arguments: []Argument{
				{Name: "ticket", Description: "Jira issue key, for example ABC-123.", Required: true},
				{Name: "attachment", Description: "Optional attachment ID or exact filename to download."},
				worktreeArgument,
			},
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, arguments Arguments) (string, error) {
				return jiraTicket(ctx, repo, values, arguments.String("ticket"), arguments.String("attachment"))
			}),
		},
		{
			Name:        "confluence",
			Description: "Return compact Confluence page context by page ID or page URL.",
			Arguments: []Argument{
				{Name: "page", Description: "Confluence page ID or supported same-host page URL.", Required: true},
				{Name: "attachment", Description: "Optional attachment ID or exact filename to download."},
				worktreeArgument,
			},
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, arguments Arguments) (string, error) {
				return confluencePage(ctx, repo, values, arguments.String("page"), arguments.String("attachment"))
			}),
		},
		{
			Name:        "sonar",
			Description: "Return failed quality-gate conditions, actionable new-code coverage lines, and confirmed/open SonarQube issues.",
			Arguments: []Argument{
				{Name: "branch", Description: "Optional Git branch name. Omit it to use the current branch."},
				worktreeArgument,
			},
			Handler: withWorktree(func(ctx context.Context, repo *git.Repo, values config.Values, arguments Arguments) (string, error) {
				return sonarIssues(ctx, repo, values, arguments.String("branch"))
			}),
		},
	}
}

func withWorktree(handler func(context.Context, *git.Repo, config.Values, Arguments) (string, error)) Handler {
	return func(ctx context.Context, arguments Arguments) (string, error) {
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
	result := map[string]any{}
	buildURL = strings.TrimSpace(buildURL)
	if buildURL == "" {
		repoContext, contextErr := repo.Context(ctx)
		if contextErr != nil {
			return "", contextErr
		}
		selection, selectionErr := selectGitLabCommit(ctx, repoContext, values)
		if selectionErr != nil {
			return "", selectionErr
		}
		buildURL, _, err = selection.client.CommitStatus(ctx, selection.project, selection.commit, jenkins.BuildStatusName(values))
		if err != nil {
			return "", err
		}
		result = jsonutil.Merge(result, selection.Map())
	}

	report, err := client.BuildReport(ctx, buildURL)
	if err != nil {
		return "", err
	}
	return jsonutil.Compact(jsonutil.Merge(result, report))
}

type gitLabCommitSelection struct {
	client          *gitlab.Client
	project         string
	commit          string
	mergeRequestIid any
}

func selectGitLabCommit(ctx context.Context, repoContext git.Context, values config.Values) (gitLabCommitSelection, error) {
	client, err := gitlab.FromValues(values)
	if err != nil {
		return gitLabCommitSelection{}, err
	}
	project := values.ValueOr(repoContext.Project, "GITLAB_PROJECT_ID")
	if project == "" {
		return gitLabCommitSelection{}, fmt.Errorf("no GitLab project found for the origin remote; set GITLAB_PROJECT_ID")
	}
	mergeRequest, _, err := client.MergeRequestLookup(ctx, repoContext)
	if err != nil {
		return gitLabCommitSelection{}, err
	}
	compactMergeRequest := jsonutil.Map(mergeRequest)
	commit := jsonutil.String(compactMergeRequest["sha"])
	if commit == "" {
		commit = repoContext.Commit
	}
	if commit == "" {
		return gitLabCommitSelection{}, fmt.Errorf("no commit found for the selected GitLab merge request or worktree")
	}
	return gitLabCommitSelection{
		client:          client,
		project:         project,
		commit:          commit,
		mergeRequestIid: compactMergeRequest["iid"],
	}, nil
}

func (s gitLabCommitSelection) Map() map[string]any {
	result := map[string]any{"commit": s.commit}
	if s.mergeRequestIid != nil {
		result["mr"] = s.mergeRequestIid
	}
	return result
}

// ciGateRuns exposes the structured GitLab records that bridge an MR commit to
// its external CI systems. It intentionally does not scrape MR comments.
func ciGateRuns(ctx context.Context, repo *git.Repo, values config.Values) (string, error) {
	repoContext, err := repo.Context(ctx)
	if err != nil {
		return "", err
	}
	selection, err := selectGitLabCommit(ctx, repoContext, values)
	if err != nil {
		return "", err
	}
	statuses, err := selection.client.CommitStatuses(ctx, selection.project, selection.commit)
	if err != nil {
		return "", err
	}
	gates := make([]any, 0, len(statuses))
	for _, status := range statuses {
		gates = append(gates, gateRun(status))
	}
	return jsonutil.Compact(jsonutil.Merge(selection.Map(), map[string]any{"gates": gates}))
}

func gateRun(status map[string]any) map[string]any {
	run := map[string]any{
		"gate":  status["name"],
		"state": status["status"],
		"url":   status["target_url"],
	}
	for _, key := range []string{"id", "description", "pipeline_id", "author", "created_at", "started_at", "finished_at", "ref"} {
		if status[key] != nil {
			run[key] = status[key]
		}
	}
	return run
}

func jiraTicket(ctx context.Context, repo *git.Repo, values config.Values, ticket, attachment string) (string, error) {
	client, err := jira.FromValues(values)
	if err != nil {
		return "", err
	}
	issue, err := client.IssueWithAttachment(ctx, ticket, repo.Root(), attachment)
	if err != nil {
		return "", err
	}
	return jsonutil.Compact(issue)
}

func confluencePage(ctx context.Context, repo *git.Repo, values config.Values, page, attachment string) (string, error) {
	client, err := confluence.FromValues(values)
	if err != nil {
		return "", err
	}
	result, err := client.PageWithAttachment(ctx, page, repo.Root(), attachment)
	if err != nil {
		return "", err
	}
	return jsonutil.Compact(result)
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
	projectKey, source, err := sonarProjectKey(ctx, repo, values)
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
	return jsonutil.Compact(issues)
}

// sonarProjectKey uses explicit configuration first. When it is absent, it
// accepts only a Sonar URL on a GitLab status for the selected MR head SHA.
func sonarProjectKey(ctx context.Context, repo *git.Repo, values config.Values) (string, string, error) {
	if projectKey := values.Value("SONAR_PROJECT_KEY"); projectKey != "" {
		return projectKey, "environment", nil
	}
	repoContext, err := repo.Context(ctx)
	if err != nil {
		return "", "", err
	}
	return sonarProjectKeyFromGitLab(ctx, repoContext, values)
}

func sonarProjectKeyFromGitLab(ctx context.Context, repoContext git.Context, values config.Values) (string, string, error) {
	baseURL, err := sonar.BaseURLFromValues(values)
	if err != nil {
		return "", "", err
	}
	selection, err := selectGitLabCommit(ctx, repoContext, values)
	if err != nil {
		return "", "", err
	}
	statuses, err := selection.client.CommitStatuses(ctx, selection.project, selection.commit)
	if err != nil {
		return "", "", err
	}
	candidates := map[string]struct{}{}
	for _, status := range statuses {
		if !strings.Contains(strings.ToLower(jsonutil.String(status["name"])), "sonar") {
			continue
		}
		if projectKey, ok := sonar.ProjectKeyFromURL(baseURL, jsonutil.String(status["target_url"])); ok {
			candidates[projectKey] = struct{}{}
		}
	}
	if len(candidates) == 1 {
		for projectKey := range candidates {
			return projectKey, "gitlab_commit_status", nil
		}
	}
	if len(candidates) > 1 {
		return "", "", fmt.Errorf("multiple SonarQube project keys found in GitLab statuses for commit %s; set SONAR_PROJECT_KEY", selection.commit)
	}
	return "", "", fmt.Errorf("no SonarQube project key found in GitLab statuses for commit %s; set SONAR_PROJECT_KEY", selection.commit)
}
