// Package gitlab reads merge request, review, and commit status context from a
// GitLab instance.
package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"github.com/jhoogstraat/ai-development-boost/internal/config"
	"github.com/jhoogstraat/ai-development-boost/internal/git"
	"github.com/jhoogstraat/ai-development-boost/internal/httpx"
	"github.com/jhoogstraat/ai-development-boost/internal/jsonutil"
)

const pageSize = 100

// Options configures a Client.
type Options struct {
	// BaseURL is the GitLab API root, for example https://gitlab.example/api/v4.
	BaseURL string
	// Token is a personal access token sent as PRIVATE-TOKEN.
	Token string
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
		ignoredAuthors: options.IgnoredAuthors,
		http:           client,
	}, nil
}

// FromEnv builds a client from GITLAB_API_URL, GITLAB_TOKEN, and the optional
// GITLAB_IGNORED_REVIEW_AUTHORS list.
func FromEnv() (*Client, error) {
	baseURL, err := config.Require("GITLAB_API_URL")
	if err != nil {
		return nil, err
	}
	token, err := config.Require("GITLAB_TOKEN")
	if err != nil {
		return nil, err
	}
	return New(Options{
		BaseURL:        baseURL,
		Token:          token,
		IgnoredAuthors: config.List("GITLAB_IGNORED_REVIEW_AUTHORS"),
	})
}

// Request performs an API call. When optional is set, a 404 yields a nil body
// instead of an error.
func (c *Client) Request(ctx context.Context, method, path string, query url.Values, optional bool) ([]byte, error) {
	response, err := httpx.Do(ctx, c.http, method, httpx.WithQuery(httpx.Join(c.baseURL, path), query), map[string]string{
		"Accept":        "application/json",
		"PRIVATE-TOKEN": c.token,
	}, 0)
	if err != nil {
		return nil, err
	}
	if optional && response.Status == http.StatusNotFound {
		return nil, nil
	}
	if !response.OK() {
		return nil, httpx.APIError("GitLab", response.Status, response.Body)
	}
	return response.Body, nil
}

func (c *Client) requestList(ctx context.Context, path string, query url.Values, description string) ([]map[string]any, error) {
	body, err := c.Request(ctx, http.MethodGet, path, query, false)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := httpx.DecodeJSON(body, &items, description); err != nil {
		return nil, err
	}
	return items, nil
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
		pageMatches, err := c.requestList(ctx, "/projects/"+ProjectPath(repo.Project)+"/merge_requests", query, "GitLab merge requests")
		if err != nil {
			return nil, err
		}
		matches = append(matches, pageMatches...)
		if len(pageMatches) < pageSize {
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

// MergeRequestLookup reports which merge request was selected for the branch of
// repo and why, together with the other candidates.
func (c *Client) MergeRequestLookup(ctx context.Context, repo git.Context) (selected any, lookup map[string]any, err error) {
	lookup = map[string]any{
		"project":       repo.Project,
		"source_branch": jsonutil.Nullable(repo.Branch),
		"selection":     "open_preferred",
	}
	if repo.Branch == "" {
		lookup["all_matches"] = 0
		lookup["open_matches"] = 0
		lookup["reason"] = "detached_head"
		lookup["related_merge_requests"] = []any{}
		return nil, lookup, nil
	}

	matches, err := c.MergeRequests(ctx, repo, true)
	if err != nil {
		return nil, nil, err
	}
	opened := make([]map[string]any, 0)
	for _, match := range matches {
		if match["state"] == "opened" {
			opened = append(opened, match)
		}
	}
	if len(opened) > 1 {
		return nil, nil, errAmbiguousMergeRequest
	}

	ordered := append([]map[string]any(nil), matches...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return fmt.Sprint(ordered[left]["updated_at"]) > fmt.Sprint(ordered[right]["updated_at"])
	})

	var candidate map[string]any
	switch {
	case len(opened) > 0:
		candidate = opened[0]
	case len(ordered) > 0:
		candidate = ordered[0]
	}

	related := make([]any, 0, len(ordered))
	for _, match := range ordered {
		related = append(related, CompactMergeRequest(match))
	}
	lookup["all_matches"] = len(matches)
	lookup["open_matches"] = len(opened)
	lookup["reason"] = lookupReason(candidate, len(matches))
	lookup["related_merge_requests"] = related
	if candidate == nil {
		lookup["selected_state"] = nil
		return nil, lookup, nil
	}
	lookup["selected_state"] = candidate["state"]
	return CompactMergeRequest(candidate), lookup, nil
}

func lookupReason(candidate map[string]any, matches int) string {
	switch {
	case candidate != nil && candidate["state"] == "opened":
		return "open_merge_request"
	case candidate != nil:
		return "matching_non_open_merge_request"
	case matches > 0:
		return "no_selectable_merge_request"
	default:
		return "no_matching_merge_request"
	}
}

// Discussions returns every discussion thread of a merge request.
func (c *Client) Discussions(ctx context.Context, repo git.Context, mergeRequest map[string]any) ([]map[string]any, error) {
	iid := url.PathEscape(fmt.Sprint(mergeRequest["iid"]))
	path := "/projects/" + ProjectPath(repo.Project) + "/merge_requests/" + iid + "/discussions"

	var all []map[string]any
	for page := 1; ; page++ {
		query := url.Values{"per_page": {fmt.Sprint(pageSize)}, "page": {fmt.Sprint(page)}}
		pageItems, err := c.requestList(ctx, path, query, "GitLab discussions")
		if err != nil {
			return nil, err
		}
		all = append(all, pageItems...)
		if len(pageItems) < pageSize {
			return all, nil
		}
	}
}

// CommitStatus returns the target URL and a compact view of the most recent
// commit status named statusName, which is how a CI build is located for a
// commit. The project is a path or numeric ID.
func (c *Client) CommitStatus(ctx context.Context, project, commit, statusName string) (targetURL string, status map[string]any, err error) {
	path := "/projects/" + ProjectPath(project) + "/repository/commits/" + url.PathEscape(commit) + "/statuses"
	statuses, err := c.requestList(ctx, path, url.Values{"all": {"true"}, "per_page": {fmt.Sprint(pageSize)}}, "GitLab commit statuses")
	if err != nil {
		return "", nil, err
	}

	var selected map[string]any
	for _, candidate := range statuses {
		if candidate["name"] != statusName || jsonutil.String(candidate["target_url"]) == "" {
			continue
		}
		if selected == nil || jsonutil.String(candidate["created_at"]) > jsonutil.String(selected["created_at"]) {
			selected = candidate
		}
	}
	if selected == nil {
		return "", nil, fmt.Errorf("no %q commit status found for commit %s", statusName, commit)
	}

	status = map[string]any{}
	for _, key := range []string{"status", "description", "target_url", "created_at", "finished_at"} {
		if selected[key] != nil {
			status[key] = selected[key]
		}
	}
	return jsonutil.String(selected["target_url"]), status, nil
}
