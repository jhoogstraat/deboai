package vcs

import (
	"context"
	"fmt"

	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/jsonutil"
)

// ReviewContext returns the open change and every actionable review comment
// for the current branch when an open change exists.
func ReviewContext(ctx context.Context, provider Provider, repo git.Context) (map[string]any, error) {
	result := map[string]any{
		"mr":      nil,
		"reviews": nil,
	}
	if repo.Branch == "" {
		return result, nil
	}
	change, err := provider.OpenChange(ctx, repo)
	if err != nil {
		return nil, err
	}
	if change == nil {
		return result, nil
	}
	reviews, err := provider.Reviews(ctx, repo, change)
	if err != nil {
		return nil, err
	}
	result["mr"] = change
	if len(reviews) > 0 {
		result["reviews"] = reviews
	}
	return result, nil
}

// Selection identifies the commit whose CI state the tools report on: the open
// change's head SHA when one exists, falling back to the local checkout.
type Selection struct {
	Provider Provider
	Project  string
	Commit   string
	ChangeID any
}

// SelectCommit resolves the project and commit the provider should be queried
// for.
func SelectCommit(ctx context.Context, provider Provider, repo git.Context) (Selection, error) {
	project, err := provider.Project(repo)
	if err != nil {
		return Selection{}, err
	}
	change, err := provider.OpenChange(ctx, repo)
	if err != nil {
		return Selection{}, err
	}
	commit := jsonutil.String(change["sha"])
	if commit == "" {
		commit = repo.Commit
	}
	if commit == "" {
		return Selection{}, fmt.Errorf("no commit found for the selected merge request or worktree")
	}
	return Selection{
		Provider: provider,
		Project:  project,
		Commit:   commit,
		ChangeID: change["iid"],
	}, nil
}

// Map renders the selection as the JSON object embedded in tool results.
func (s Selection) Map() map[string]any {
	result := map[string]any{"commit": s.Commit}
	if s.ChangeID != nil {
		result["mr"] = s.ChangeID
	}
	return result
}

// CommitStatus returns the target URL and a compact view of the most recent
// commit status named statusName, which is how a CI build is located for a
// commit.
func CommitStatus(ctx context.Context, provider Provider, project, commit, statusName string) (targetURL string, status map[string]any, err error) {
	statuses, err := provider.CommitStatuses(ctx, project, commit)
	if err != nil {
		return "", nil, err
	}

	var selected map[string]any
	for _, candidate := range statuses {
		if candidate["name"] != statusName {
			continue
		}
		if selected != nil {
			return "", nil, fmt.Errorf("multiple current %q commit statuses found for commit %s", statusName, commit)
		}
		selected = candidate
	}
	if selected == nil {
		return "", nil, fmt.Errorf("no %q commit status found for commit %s", statusName, commit)
	}
	targetURL = jsonutil.String(selected["target_url"])
	if targetURL == "" {
		return "", nil, fmt.Errorf("current %q commit status for commit %s has no target URL", statusName, commit)
	}
	return targetURL, selected, nil
}
