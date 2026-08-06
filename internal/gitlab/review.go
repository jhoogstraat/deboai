package gitlab

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jhoogstraat/ai-development-boost/internal/git"
	"github.com/jhoogstraat/ai-development-boost/internal/jsonutil"
	"github.com/jhoogstraat/ai-development-boost/internal/textutil"
)

const maxReviewBodyLength = 4000

// ReviewContext returns the repository context together with the latest
// actionable review comment on the open merge request of the current branch.
func (c *Client) ReviewContext(ctx context.Context, repo git.Context) (map[string]any, error) {
	if repo.Branch == "" {
		return nil, fmt.Errorf("the GitLab review context requires a named current branch")
	}
	mergeRequest, err := c.OpenMergeRequest(ctx, repo)
	if err != nil {
		return nil, err
	}
	if mergeRequest == nil {
		return nil, fmt.Errorf("no open GitLab merge request exists for the current branch")
	}
	username, err := c.CurrentUsername(ctx)
	if err != nil {
		return nil, err
	}
	discussions, err := c.Discussions(ctx, repo, mergeRequest)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"repository":    repo.Map(),
		"branch":        repo.Branch,
		"merge_request": CompactMergeRequest(mergeRequest),
		"review":        CompactReview(c.latestReview(discussions, username)),
	}, nil
}

// CompactMergeRequest reduces a merge request to the fields worth reporting.
func CompactMergeRequest(value map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"iid", "title", "state", "draft", "source_branch", "target_branch", "sha", "web_url"} {
		result[key] = value[key]
	}
	return result
}

// CompactReview reduces a review note to its body and diff position.
func CompactReview(note map[string]any) any {
	if note == nil {
		return nil
	}
	position := jsonutil.Map(note["position"])
	lineRange := jsonutil.Map(position["line_range"])
	lineEnd := jsonutil.Map(lineRange["end"])
	lineStart := jsonutil.Map(lineRange["start"])
	author := jsonutil.Map(note["author"])

	return map[string]any{
		"id":         note["id"],
		"author":     author["username"],
		"created_at": note["created_at"],
		"body":       textutil.Truncate(jsonutil.String(note["body"]), maxReviewBodyLength),
		"path":       jsonutil.FirstNonNil(position["new_path"], position["old_path"]),
		"line": jsonutil.FirstNonNil(
			lineEnd["new_line"], lineStart["new_line"], lineEnd["old_line"], lineStart["old_line"],
			position["new_line"], position["old_line"],
		),
		"position_type": position["position_type"],
		"base_sha":      position["base_sha"],
		"head_sha":      position["head_sha"],
		"resolvable":    note["resolvable"],
		"resolved":      note["resolved"],
	}
}

// latestReview picks the newest actionable note, preferring notes anchored to a
// diff position over general discussion.
func (c *Client) latestReview(discussions []map[string]any, excludedAuthor string) map[string]any {
	var candidates []map[string]any
	for _, discussion := range discussions {
		if resolved, _ := discussion["resolved"].(bool); resolved {
			continue
		}
		for _, rawNote := range jsonutil.Array(discussion, "notes") {
			note := jsonutil.Map(rawNote)
			if c.actionableNote(note, excludedAuthor) {
				candidates = append(candidates, note)
			}
		}
	}

	positioned := make([]map[string]any, 0)
	for _, candidate := range candidates {
		if _, ok := candidate["position"].(map[string]any); ok {
			positioned = append(positioned, candidate)
		}
	}
	if len(positioned) > 0 {
		candidates = positioned
	}

	var latest map[string]any
	for _, candidate := range candidates {
		if latest == nil || fmt.Sprint(candidate["created_at"]) > fmt.Sprint(latest["created_at"]) {
			latest = candidate
		}
	}
	return latest
}

func (c *Client) actionableNote(note map[string]any, excludedAuthor string) bool {
	if note == nil || note["system"] == true || strings.TrimSpace(jsonutil.String(note["body"])) == "" {
		return false
	}
	username := jsonutil.String(jsonutil.Map(note["author"])["username"])
	return username != excludedAuthor && !slices.Contains(c.ignoredAuthors, username)
}
