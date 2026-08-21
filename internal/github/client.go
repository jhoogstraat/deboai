// Package github reads pull request, review, and commit status context from
// GitHub.
package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/httpx"
	"github.com/jhoogstraat/deboai/internal/jsonutil"
)

const pageSize = 100

// DefaultBaseURL is the API root of github.com. GitHub Enterprise Server
// instances configure GITHUB_API_URL instead.
const DefaultBaseURL = "https://api.github.com"

// Options configures a Client.
type Options struct {
	// BaseURL is the GitHub API root. It defaults to DefaultBaseURL.
	BaseURL string
	// Token is a personal access token sent as a bearer token.
	Token string
	// Repo overrides the owner/repo path parsed from the origin remote.
	Repo string
	// IgnoredAuthors are usernames whose pull request comments are never
	// treated as actionable review comments, such as CI service accounts.
	IgnoredAuthors []string
	// HTTPClient overrides the default HTTP client.
	HTTPClient *http.Client
}

// Client talks to the GitHub REST API.
type Client struct {
	baseURL        string
	token          string
	repo           string
	ignoredAuthors []string
	http           *http.Client
}

// New builds a client from explicit options.
func New(options Options) (*Client, error) {
	if options.Token == "" {
		return nil, fmt.Errorf("GitHub token is required")
	}
	baseURL := options.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	client := options.HTTPClient
	if client == nil {
		client = httpx.NewClient()
	}
	return &Client{
		baseURL:        baseURL,
		token:          options.Token,
		repo:           options.Repo,
		ignoredAuthors: options.IgnoredAuthors,
		http:           client,
	}, nil
}

// FromValues builds a client from GITHUB_TOKEN and the optional
// GITHUB_API_URL, GITHUB_REPO, and GITHUB_IGNORED_REVIEW_AUTHORS values.
func FromValues(values config.Values) (*Client, error) {
	token, err := values.Require("GITHUB_TOKEN")
	if err != nil {
		return nil, err
	}
	return New(Options{
		BaseURL:        values.Value("GITHUB_API_URL"),
		Token:          token,
		Repo:           values.Value("GITHUB_REPO"),
		IgnoredAuthors: values.List("GITHUB_IGNORED_REVIEW_AUTHORS"),
	})
}

// Project resolves the GitHub repository to query, preferring the configured
// GITHUB_REPO over the owner/repo path parsed from the origin remote.
func (c *Client) Project(repo git.Context) (string, error) {
	project := c.repo
	if project == "" {
		project = repo.Project
	}
	if project == "" || !strings.Contains(project, "/") {
		return "", fmt.Errorf("no GitHub owner/repo found for the origin remote; set GITHUB_REPO")
	}
	return project, nil
}

func (c *Client) response(ctx context.Context, path string, query url.Values) (httpx.Response, error) {
	response, err := httpx.Do(ctx, c.http, http.MethodGet, httpx.WithQuery(httpx.Join(c.baseURL, path), query), map[string]string{
		"Accept":               "application/vnd.github+json",
		"Authorization":        "Bearer " + c.token,
		"X-GitHub-Api-Version": "2022-11-28",
	}, 0)
	if err != nil {
		return httpx.Response{}, err
	}
	if !response.OK() {
		return httpx.Response{}, httpx.APIError("GitHub", response.Status, response.Body)
	}
	return response, nil
}

func hasNextPage(response httpx.Response) bool {
	return strings.Contains(response.Header.Get("Link"), `rel="next"`)
}

func (c *Client) requestList(ctx context.Context, path string, query url.Values, description string) ([]map[string]any, bool, error) {
	response, err := c.response(ctx, path, query)
	if err != nil {
		return nil, false, err
	}
	var items []map[string]any
	if err := httpx.DecodeJSON(response.Body, &items, description); err != nil {
		return nil, false, err
	}
	return items, hasNextPage(response), nil
}

func (c *Client) requestAllPages(ctx context.Context, path string, query url.Values, description string) ([]map[string]any, error) {
	var all []map[string]any
	query.Set("per_page", fmt.Sprint(pageSize))
	for page := 1; ; page++ {
		query.Set("page", fmt.Sprint(page))
		pageItems, hasNext, err := c.requestList(ctx, path, query, description)
		if err != nil {
			return nil, err
		}
		all = append(all, pageItems...)
		if !hasNext && len(pageItems) < pageSize {
			return all, nil
		}
	}
}

// CurrentLogin returns the login of the authenticated account.
func (c *Client) CurrentLogin(ctx context.Context) (string, error) {
	response, err := c.response(ctx, "/user", nil)
	if err != nil {
		return "", err
	}
	var user map[string]any
	if err := httpx.DecodeJSON(response.Body, &user, "GitHub user"); err != nil {
		return "", err
	}
	login := jsonutil.String(user["login"])
	if login == "" {
		return "", fmt.Errorf("GitHub did not return the authenticated login")
	}
	return login, nil
}

// repoPath builds the /repos prefix for an owner/repo project.
func repoPath(project string) string {
	return "/repos/" + project
}

// CommitStatuses returns compact provenance for the latest state of every gate
// on a commit, combining GitHub's commit status API with the check-runs API
// that GitHub Actions publishes to. The project is an owner/repo path.
func (c *Client) CommitStatuses(ctx context.Context, project, commit string) ([]map[string]any, error) {
	compact := []map[string]any{}

	statuses, err := c.combinedStatuses(ctx, project, commit)
	if err != nil {
		return nil, err
	}
	for _, status := range statuses {
		compact = append(compact, CompactCommitStatus(status, commit))
	}

	checkRuns, err := c.checkRuns(ctx, project, commit)
	if err != nil {
		return nil, err
	}
	for _, checkRun := range checkRuns {
		compact = append(compact, CompactCheckRun(checkRun, commit))
	}
	return compact, nil
}

// combinedStatuses reads the combined status endpoint, which reports only the
// latest status per context.
func (c *Client) combinedStatuses(ctx context.Context, project, commit string) ([]map[string]any, error) {
	path := repoPath(project) + "/commits/" + url.PathEscape(commit) + "/status"
	var statuses []map[string]any
	for page := 1; ; page++ {
		query := url.Values{"per_page": {fmt.Sprint(pageSize)}, "page": {fmt.Sprint(page)}}
		response, err := c.response(ctx, path, query)
		if err != nil {
			return nil, err
		}
		var combined struct {
			Statuses []map[string]any `json:"statuses"`
		}
		if err := httpx.DecodeJSON(response.Body, &combined, "GitHub combined status"); err != nil {
			return nil, err
		}
		statuses = append(statuses, combined.Statuses...)
		if !hasNextPage(response) && len(combined.Statuses) < pageSize {
			return statuses, nil
		}
	}
}

// checkRuns reads the latest check run per name for a commit.
func (c *Client) checkRuns(ctx context.Context, project, commit string) ([]map[string]any, error) {
	path := repoPath(project) + "/commits/" + url.PathEscape(commit) + "/check-runs"
	var checkRuns []map[string]any
	for page := 1; ; page++ {
		query := url.Values{"per_page": {fmt.Sprint(pageSize)}, "page": {fmt.Sprint(page)}}
		response, err := c.response(ctx, path, query)
		if err != nil {
			return nil, err
		}
		var result struct {
			CheckRuns []map[string]any `json:"check_runs"`
		}
		if err := httpx.DecodeJSON(response.Body, &result, "GitHub check runs"); err != nil {
			return nil, err
		}
		checkRuns = append(checkRuns, result.CheckRuns...)
		if !hasNextPage(response) && len(result.CheckRuns) < pageSize {
			return checkRuns, nil
		}
	}
}

// CompactCommitStatus normalizes a GitHub commit status into the compact gate
// shape the ci tool reports.
func CompactCommitStatus(status map[string]any, commit string) map[string]any {
	compact := map[string]any{
		"name":   jsonutil.FirstNonNil(status["context"], status["name"]),
		"status": commitStatusState(jsonutil.String(status["state"])),
		"sha":    commit,
	}
	for _, key := range []string{"id", "description", "target_url", "created_at"} {
		if status[key] != nil {
			compact[key] = status[key]
		}
	}
	if creator := jsonutil.Map(status["creator"]); jsonutil.String(creator["login"]) != "" {
		compact["author"] = jsonutil.String(creator["login"])
	}
	return compact
}

// CompactCheckRun normalizes a GitHub check run into the compact gate shape
// the ci tool reports.
func CompactCheckRun(checkRun map[string]any, commit string) map[string]any {
	compact := map[string]any{
		"name":   checkRun["name"],
		"status": checkRunState(jsonutil.String(checkRun["status"]), jsonutil.String(checkRun["conclusion"])),
		"sha":    jsonutil.FirstNonNil(checkRun["head_sha"], commit),
	}
	if checkRun["id"] != nil {
		compact["id"] = checkRun["id"]
	}
	if targetURL := jsonutil.FirstString(checkRun["details_url"], checkRun["html_url"]); targetURL != "" {
		compact["target_url"] = targetURL
	}
	if title := jsonutil.String(jsonutil.Map(checkRun["output"])["title"]); title != "" {
		compact["description"] = title
	}
	if checkRun["started_at"] != nil {
		compact["started_at"] = checkRun["started_at"]
	}
	if checkRun["completed_at"] != nil {
		compact["finished_at"] = checkRun["completed_at"]
	}
	return compact
}

// commitStatusState maps GitHub commit status states onto the status
// vocabulary the ci tool prints.
func commitStatusState(state string) string {
	switch state {
	case "success":
		return "success"
	case "pending":
		return "pending"
	case "failure", "error":
		return "failed"
	default:
		return state
	}
}

// checkRunState maps a check run's status and conclusion onto the status
// vocabulary the ci tool prints.
func checkRunState(status, conclusion string) string {
	switch status {
	case "queued", "waiting", "requested", "pending":
		return "pending"
	case "in_progress":
		return "running"
	}
	switch conclusion {
	case "success":
		return "success"
	case "neutral", "skipped":
		return "skipped"
	case "cancelled":
		return "canceled"
	case "action_required":
		return "manual"
	case "failure", "timed_out", "stale":
		return "failed"
	default:
		return "failed"
	}
}
