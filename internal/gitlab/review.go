package gitlab

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/jsonutil"
	"github.com/jhoogstraat/deboai/internal/textutil"
)

const maxReviewBodyLength = 4000

// OpenChange returns the compact open merge request for the branch of repo, or
// nil when there is none.
func (c *Client) OpenChange(ctx context.Context, repo git.Context) (map[string]any, error) {
	mergeRequest, err := c.OpenMergeRequest(ctx, repo)
	if err != nil || mergeRequest == nil {
		return nil, err
	}
	return CompactMergeRequest(mergeRequest), nil
}

// Reviews returns every actionable review comment on change in compact form,
// oldest first, or nil when there are none.
func (c *Client) Reviews(ctx context.Context, repo git.Context, change map[string]any) ([]map[string]any, error) {
	username, err := c.CurrentUsername(ctx)
	if err != nil {
		return nil, err
	}
	discussions, err := c.Discussions(ctx, repo, change)
	if err != nil {
		return nil, err
	}
	return CompactReviews(c.actionableNotes(discussions, username)), nil
}

// CompactMergeRequest reduces a merge request to the fields worth reporting.
func CompactMergeRequest(value map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"iid", "title", "state", "draft", "source_branch", "target_branch", "sha", "web_url"} {
		result[key] = value[key]
	}
	return result
}

// CompactReviews reduces each review note to its body and diff position, or
// nil when there are no notes.
func CompactReviews(notes []map[string]any) []map[string]any {
	if len(notes) == 0 {
		return nil
	}
	reviews := make([]map[string]any, 0, len(notes))
	for _, note := range notes {
		reviews = append(reviews, CompactReview(note))
	}
	return reviews
}

// CompactReview reduces a review note to its body and diff position.
func CompactReview(note map[string]any) map[string]any {
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

// actionableNotes collects every actionable note of the unresolved
// discussions, oldest first.
func (c *Client) actionableNotes(discussions []map[string]any, excludedAuthor string) []map[string]any {
	var notes []map[string]any
	for _, discussion := range discussions {
		if resolved, _ := discussion["resolved"].(bool); resolved {
			continue
		}
		for _, rawNote := range jsonutil.Array(discussion, "notes") {
			note := jsonutil.Map(rawNote)
			if c.actionableNote(note, excludedAuthor) {
				notes = append(notes, note)
			}
		}
	}
	slices.SortStableFunc(notes, func(a, b map[string]any) int {
		return strings.Compare(fmt.Sprint(a["created_at"]), fmt.Sprint(b["created_at"]))
	})
	return notes
}

func (c *Client) actionableNote(note map[string]any, excludedAuthor string) bool {
	if note == nil || note["system"] == true || strings.TrimSpace(jsonutil.String(note["body"])) == "" {
		return false
	}
	username := jsonutil.String(jsonutil.Map(note["author"])["username"])
	return username != excludedAuthor && !slices.Contains(c.ignoredAuthors, username)
}
