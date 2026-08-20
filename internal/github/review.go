package github

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/jsonutil"
	"github.com/jhoogstraat/deboai/internal/textutil"
)

const maxReviewBodyLength = 4000

// OpenChange returns the compact open pull request for the branch of repo, or
// nil when there is none.
func (c *Client) OpenChange(ctx context.Context, repo git.Context) (map[string]any, error) {
	pullRequest, err := c.OpenPullRequest(ctx, repo)
	if err != nil || pullRequest == nil {
		return nil, err
	}
	return CompactPullRequest(pullRequest), nil
}

// OpenPullRequest returns the single open pull request whose head is the
// branch of repo, or nil when there is none.
func (c *Client) OpenPullRequest(ctx context.Context, repo git.Context) (map[string]any, error) {
	if repo.Branch == "" {
		return nil, nil
	}
	project, err := c.Project(repo)
	if err != nil {
		return nil, err
	}
	owner, _, _ := strings.Cut(project, "/")
	query := url.Values{
		"head":  {owner + ":" + repo.Branch},
		"state": {"open"},
	}
	matches, err := c.requestAllPages(ctx, repoPath(project)+"/pulls", query, "GitHub pull requests")
	if err != nil {
		return nil, err
	}
	if len(matches) > 1 {
		return nil, errAmbiguousPullRequest
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return matches[0], nil
}

var errAmbiguousPullRequest = fmt.Errorf("more than one open GitHub pull request exists for the current branch")

// CompactPullRequest reduces a pull request to the fields worth reporting,
// matching the compact merge request shape.
func CompactPullRequest(value map[string]any) map[string]any {
	head := jsonutil.Map(value["head"])
	base := jsonutil.Map(value["base"])
	return map[string]any{
		"iid":           value["number"],
		"title":         value["title"],
		"state":         value["state"],
		"draft":         value["draft"],
		"source_branch": head["ref"],
		"target_branch": base["ref"],
		"sha":           head["sha"],
		"web_url":       value["html_url"],
	}
}

// Reviews returns every actionable review comment on change in compact form,
// oldest first, or nil when there are none. Diff-anchored pull request review
// comments and general issue comments are both included.
func (c *Client) Reviews(ctx context.Context, repo git.Context, change map[string]any) ([]map[string]any, error) {
	project, err := c.Project(repo)
	if err != nil {
		return nil, err
	}
	login, err := c.CurrentLogin(ctx)
	if err != nil {
		return nil, err
	}
	number := url.PathEscape(fmt.Sprint(change["iid"]))

	reviewComments, err := c.requestAllPages(ctx, repoPath(project)+"/pulls/"+number+"/comments", url.Values{}, "GitHub review comments")
	if err != nil {
		return nil, err
	}
	issueComments, err := c.requestAllPages(ctx, repoPath(project)+"/issues/"+number+"/comments", url.Values{}, "GitHub issue comments")
	if err != nil {
		return nil, err
	}

	comments := c.actionableComments(reviewComments, login)
	comments = append(comments, c.actionableComments(issueComments, login)...)
	slices.SortStableFunc(comments, func(a, b map[string]any) int {
		return strings.Compare(fmt.Sprint(a["created_at"]), fmt.Sprint(b["created_at"]))
	})
	return CompactReviewComments(comments), nil
}

func (c *Client) actionableComments(comments []map[string]any, excludedAuthor string) []map[string]any {
	var actionable []map[string]any
	for _, comment := range comments {
		if strings.TrimSpace(jsonutil.String(comment["body"])) == "" {
			continue
		}
		login := jsonutil.String(jsonutil.Map(comment["user"])["login"])
		if login == excludedAuthor || slices.Contains(c.ignoredAuthors, login) {
			continue
		}
		actionable = append(actionable, comment)
	}
	return actionable
}

// CompactReviewComments reduces each comment to its body and diff position, or
// nil when there are no comments.
func CompactReviewComments(comments []map[string]any) []map[string]any {
	if len(comments) == 0 {
		return nil
	}
	reviews := make([]map[string]any, 0, len(comments))
	for _, comment := range comments {
		reviews = append(reviews, CompactReviewComment(comment))
	}
	return reviews
}

// CompactReviewComment reduces a comment to its body and diff position,
// matching the compact review shape. Fields GitHub does not expose over REST,
// such as the diff base SHA and thread resolution, are omitted.
func CompactReviewComment(comment map[string]any) map[string]any {
	if comment == nil {
		return nil
	}
	compact := map[string]any{
		"id":         comment["id"],
		"author":     jsonutil.Map(comment["user"])["login"],
		"created_at": comment["created_at"],
		"body":       textutil.Truncate(jsonutil.String(comment["body"]), maxReviewBodyLength),
	}
	if path := jsonutil.FirstNonNil(comment["path"], comment["original_path"]); path != nil {
		compact["path"] = path
		compact["line"] = jsonutil.FirstNonNil(
			comment["line"], comment["start_line"], comment["original_line"], comment["original_start_line"],
		)
	}
	if headSHA := jsonutil.String(comment["commit_id"]); headSHA != "" {
		compact["head_sha"] = headSHA
	}
	return compact
}
