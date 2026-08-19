// Package vcs selects the version control backend that resolves review and CI
// context for a repository, and hosts the resolution logic shared by every
// backend.
package vcs

import (
	"context"
	"fmt"
	"strings"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/github"
	"github.com/jhoogstraat/deboai/internal/gitlab"
)

// Provider fetches review and CI state from one version control host. Compact
// maps carry the provider-normalized fields the tools report, including the
// change identifier under "iid" and the change head commit under "sha".
type Provider interface {
	// Project resolves the remote project to query, preferring explicit
	// provider configuration over the project parsed from the origin remote.
	Project(repo git.Context) (string, error)
	// OpenChange returns the compact open change (merge request) for the
	// branch of repo, or nil when there is none.
	OpenChange(ctx context.Context, repo git.Context) (map[string]any, error)
	// LatestReview returns the latest actionable review comment on the open
	// change in compact form, or nil when there is none.
	LatestReview(ctx context.Context, repo git.Context, change map[string]any) (any, error)
	// CommitStatuses returns compact provenance for the latest status of
	// every CI gate on a commit.
	CommitStatuses(ctx context.Context, project, commit string) ([]map[string]any, error)
}

// FromValues returns the backend for the repository's origin remote host.
// GitHub- and GitLab-named hosts pick their backend directly; other hosts fall
// back to whichever backend is explicitly configured.
func FromValues(values config.Values, host string) (Provider, error) {
	switch {
	case strings.Contains(strings.ToLower(host), "github"):
		return github.FromValues(values)
	case strings.Contains(strings.ToLower(host), "gitlab"):
		return gitlab.FromValues(values)
	case values.Value("GITHUB_API_URL") != "":
		return github.FromValues(values)
	case values.Value("GITLAB_API_URL") != "":
		return gitlab.FromValues(values)
	case host == "":
		return nil, fmt.Errorf("no origin remote host found and no VCS backend configured; set GITLAB_API_URL or GITHUB_API_URL")
	default:
		return nil, fmt.Errorf("unsupported VCS host %q; set GITLAB_API_URL or GITHUB_API_URL to pick a backend", host)
	}
}
