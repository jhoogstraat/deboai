// Package gitlab reads merge request, review, and commit status context from a
// GitLab instance.
package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/httpx"
	"github.com/jhoogstraat/deboai/internal/jsonutil"
)

const pageSize = 100

// Options configures a Client.
type Options struct {
	// BaseURL is the GitLab API root, for example https://gitlab.example/api/v4.
	BaseURL string
	// Token is a personal access token sent as PRIVATE-TOKEN.
	Token string
	// Project overrides the project path or numeric ID parsed from the
	// origin remote.
	Project string
	// IgnoredAuthors are usernames whose merge request notes are never
	// treated as actionable review comments, such as CI service accounts.
	IgnoredAuthors []string
	// HTTPClient overrides the default HTTP client.
	HTTPClient *http.Client
}

// Client talks to the GitLab REST API.
type Client struct {
	baseURL        string
	token          string
	project        string
	ignoredAuthors []string
	http           *http.Client
}

// New builds a client from explicit options.
func New(options Options) (*Client, error) {
	if options.BaseURL == "" {
		return nil, fmt.Errorf("GitLab API URL is required")
	}
	if options.Token == "" {
		return nil, fmt.Errorf("GitLab token is required")
	}
	client := options.HTTPClient
	if client == nil {
		client = httpx.NewClient()
	}
	return &Client{
		baseURL:        options.BaseURL,
		token:          options.Token,
		project:        options.Project,
		ignoredAuthors: options.IgnoredAuthors,
		http:           client,
	}, nil
}

// FromValues builds a client from GITLAB_API_URL, GITLAB_TOKEN, and the
// optional GITLAB_PROJECT_ID and GITLAB_IGNORED_REVIEW_AUTHORS values.
func FromValues(values config.Values) (*Client, error) {
	baseURL, err := values.Require("GITLAB_API_URL")
	if err != nil {
		return nil, err
	}
	token, err := values.Require("GITLAB_TOKEN")
	if err != nil {
		return nil, err
	}
	return New(Options{
		BaseURL:        baseURL,
		Token:          token,
		Project:        values.Value("GITLAB_PROJECT_ID"),
		IgnoredAuthors: values.List("GITLAB_IGNORED_REVIEW_AUTHORS"),
	})
}

// Project resolves the GitLab project to query, preferring the configured
// GITLAB_PROJECT_ID over the project parsed from the origin remote.
func (c *Client) Project(repo git.Context) (string, error) {
	project := c.project
	if project == "" {
		project = repo.Project
	}
	if project == "" {
		return "", fmt.Errorf("no GitLab project found for the origin remote; set GITLAB_PROJECT_ID")
	}
	return project, nil
}

// Request performs an API call. When optional is set, a 404 yields a nil body
// instead of an error.
func (c *Client) Request(ctx context.Context, method, path string, query url.Values, optional bool) ([]byte, error) {
	response, err := c.response(ctx, method, path, query, optional)
	if err != nil {
		return nil, err
	}
	if optional && response.Status == http.StatusNotFound {
		return nil, nil
	}
	return response.Body, nil
}

func (c *Client) response(ctx context.Context, method, path string, query url.Values, optional bool) (httpx.Response, error) {
	response, err := httpx.Do(ctx, c.http, method, httpx.WithQuery(httpx.Join(c.baseURL, path), query), map[string]string{
		"Accept":        "application/json",
		"PRIVATE-TOKEN": c.token,
	}, 0)
	if err != nil {
		return httpx.Response{}, err
	}
	if optional && response.Status == http.StatusNotFound {
		return response, nil
	}
	if !response.OK() {
		return httpx.Response{}, httpx.APIError("GitLab", response.Status, response.Body)
	}
	return response, nil
}

func (c *Client) requestList(ctx context.Context, path string, query url.Values, description string) ([]map[string]any, bool, error) {
	response, err := c.response(ctx, http.MethodGet, path, query, false)
	if err != nil {
		return nil, false, err
	}
	var items []map[string]any
	if err := httpx.DecodeJSON(response.Body, &items, description); err != nil {
		return nil, false, err
	}
	return items, response.Header.Get("X-Next-Page") != "", nil
}

// CurrentUsername returns the username of the authenticated account.
func (c *Client) CurrentUsername(ctx context.Context) (string, error) {
	body, err := c.Request(ctx, http.MethodGet, "/user", nil, false)
	if err != nil {
		return "", err
	}
	var user map[string]any
	if err := httpx.DecodeJSON(body, &user, "GitLab user"); err != nil {
		return "", err
	}
	username := jsonutil.String(user["username"])
	if username == "" {
		return "", fmt.Errorf("GitLab did not return the authenticated username")
	}
	return username, nil
}

// ProjectPath escapes a project path or numeric ID for use in an API path.
func ProjectPath(project string) string {
	if project == "" {
		return ":fullpath"
	}
	return url.PathEscape(project)
}

// MergeRequests returns every merge request whose source branch is the branch
// of repo, optionally including closed and merged ones.
func (c *Client) MergeRequests(ctx context.Context, repo git.Context, allStates bool) ([]map[string]any, error) {
	if repo.Branch == "" {
		return []map[string]any{}, nil
	}
	state := "opened"
	if allStates {
		state = "all"
	}
	query := url.Values{
		"source_branch": {repo.Branch},
		"per_page":      {fmt.Sprint(pageSize)},
		"state":         {state},
	}

	var matches []map[string]any
	for page := 1; ; page++ {
		query.Set("page", fmt.Sprint(page))
		pageMatches, hasNext, err := c.requestList(ctx, "/projects/"+ProjectPath(repo.Project)+"/merge_requests", query, "GitLab merge requests")
		if err != nil {
			return nil, err
		}
		matches = append(matches, pageMatches...)
		if !hasNext && len(pageMatches) < pageSize {
			return matches, nil
		}
	}
}

// OpenMergeRequest returns the single open merge request for the branch of
// repo, or nil when there is none.
func (c *Client) OpenMergeRequest(ctx context.Context, repo git.Context) (map[string]any, error) {
	matches, err := c.MergeRequests(ctx, repo, false)
	if err != nil {
		return nil, err
	}
	if len(matches) > 1 {
		return nil, errAmbiguousMergeRequest
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return matches[0], nil
}

var errAmbiguousMergeRequest = fmt.Errorf("more than one open GitLab merge request exists for the current branch")

// Discussions returns every discussion thread of a merge request.
func (c *Client) Discussions(ctx context.Context, repo git.Context, mergeRequest map[string]any) ([]map[string]any, error) {
	iid := url.PathEscape(fmt.Sprint(mergeRequest["iid"]))
	path := "/projects/" + ProjectPath(repo.Project) + "/merge_requests/" + iid + "/discussions"

	var all []map[string]any
	for page := 1; ; page++ {
		query := url.Values{"per_page": {fmt.Sprint(pageSize)}, "page": {fmt.Sprint(page)}}
		pageItems, hasNext, err := c.requestList(ctx, path, query, "GitLab discussions")
		if err != nil {
			return nil, err
		}
		all = append(all, pageItems...)
		if !hasNext && len(pageItems) < pageSize {
			return all, nil
		}
	}
}

// CommitStatuses returns compact provenance for the latest status of every
// gate on a commit. The project is a path or numeric ID. Calling this endpoint
// with the merge request head SHA is the stable bridge to external CI.
func (c *Client) CommitStatuses(ctx context.Context, project, commit string) ([]map[string]any, error) {
	path := "/projects/" + ProjectPath(project) + "/repository/commits/" + url.PathEscape(commit) + "/statuses"
	statuses := []map[string]any{}
	for page := 1; ; page++ {
		query := url.Values{"per_page": {fmt.Sprint(pageSize)}, "page": {fmt.Sprint(page)}}
		pageStatuses, hasNext, err := c.requestList(ctx, path, query, "GitLab commit statuses")
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, pageStatuses...)
		if !hasNext && len(pageStatuses) < pageSize {
			break
		}
	}
	compact := make([]map[string]any, 0, len(statuses))
	for _, status := range statuses {
		compact = append(compact, CompactCommitStatus(status, commit))
	}
	return compact, nil
}

// CompactCommitStatus keeps the identity, target, and timing fields needed to
// correlate an external CI run without returning GitLab's full status record.
func CompactCommitStatus(status map[string]any, commit string) map[string]any {
	compact := map[string]any{}
	for _, key := range []string{"id", "name", "status", "description", "target_url", "created_at", "started_at", "finished_at", "sha", "ref", "pipeline_id"} {
		if status[key] != nil {
			compact[key] = status[key]
		}
	}
	if compact["sha"] == nil && commit != "" {
		compact["sha"] = commit
	}
	if compact["pipeline_id"] == nil {
		if pipeline := jsonutil.Map(status["pipeline"]); pipeline != nil && pipeline["id"] != nil {
			compact["pipeline_id"] = pipeline["id"]
		}
	}
	if author := jsonutil.Map(status["author"]); author != nil && jsonutil.String(author["username"]) != "" {
		compact["author"] = jsonutil.String(author["username"])
	}
	return compact
}
