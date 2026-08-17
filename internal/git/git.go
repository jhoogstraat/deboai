// Package git reads the local repository context the tools report on.
package git

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/jsonutil"
)

// RootVariable pins the repository the process inspects instead of discovering
// it from the working directory.
const RootVariable = "DEBOAI_REPOSITORY_ROOT"

// Repo runs git commands inside a single repository.
type Repo struct {
	root string
}

// Open returns a Repo rooted at the given absolute path.
func Open(root string) *Repo {
	return &Repo{root: root}
}

// Root returns the repository root.
func (r *Repo) Root() string {
	return r.root
}

// DiscoverRoot resolves the repository root from RootVariable, falling back to
// the git repository containing the working directory.
func DiscoverRoot() (string, error) {
	path, err := defaultWorktreePath()
	if err != nil {
		return "", err
	}
	return discoverRoot(context.Background(), path)
}

// OpenWorktree resolves path to its Git worktree root and opens that worktree.
// An empty path resolves the configured repository or current working directory.
func OpenWorktree(ctx context.Context, path string) (*Repo, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		if path, err = defaultWorktreePath(); err != nil {
			return nil, err
		}
	}
	root, err := discoverRoot(ctx, path)
	if err != nil {
		return nil, err
	}
	return Open(root), nil
}

func defaultWorktreePath() (string, error) {
	if path := config.Value(RootVariable); path != "" {
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("check %s: %w", RootVariable, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%s is not a directory: %s", RootVariable, path)
		}
		return path, nil
	}
	path, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return path, nil
}

func discoverRoot(ctx context.Context, path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve worktree path: %w", err)
	}
	command := exec.CommandContext(ctx, "git", "-C", absolute, "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("find repository root for %s: %w", absolute, err)
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", errors.New("git returned an empty repository root")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("canonicalize repository root: %w", err)
	}
	return root, nil
}

// Run executes a git command in the repository and returns its trimmed output.
func (r *Repo) Run(ctx context.Context, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = r.root
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("command failed (git %s): %w", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (r *Repo) optional(ctx context.Context, arguments ...string) string {
	value, err := r.Run(ctx, arguments...)
	if err != nil {
		return ""
	}
	return value
}

// CurrentBranch returns the checked out branch, or the empty string on a
// detached HEAD.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	return r.Run(ctx, "branch", "--show-current")
}

// Context describes the repository state reported alongside every tool result.
type Context struct {
	Project    string
	RemoteHost string
	Branch     string
	Upstream   string
	Commit     string
	Worktree   string
	Dirty      bool
}

// Map renders the context as the JSON object embedded in tool results.
func (c Context) Map() map[string]any {
	result := map[string]any{
		"remote": map[string]any{
			"host":    c.RemoteHost,
			"project": c.Project,
		},
		"branch":   jsonutil.Nullable(c.Branch),
		"upstream": jsonutil.Nullable(c.Upstream),
		"commit":   c.Commit,
		"dirty":    c.Dirty,
	}
	if c.Worktree != "" {
		result["worktree"] = c.Worktree
	}
	return result
}

// Context collects the branch, commit, remote, and worktree state.
func (r *Repo) Context(ctx context.Context) (Context, error) {
	commit, err := r.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return Context{}, err
	}
	status, err := r.Run(ctx, "status", "--porcelain=v1")
	if err != nil {
		return Context{}, err
	}
	worktree, err := r.worktreeName(ctx)
	if err != nil {
		return Context{}, err
	}
	host, project := RemoteParts(r.optional(ctx, "remote", "get-url", "origin"))

	return Context{
		Project:    project,
		RemoteHost: host,
		Branch:     r.optional(ctx, "symbolic-ref", "--quiet", "--short", "HEAD"),
		Upstream:   r.optional(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"),
		Commit:     commit,
		Worktree:   worktree,
		Dirty:      status != "",
	}, nil
}

// worktreeName returns the name Git assigned to a linked worktree, or the
// empty string for the main worktree.
func (r *Repo) worktreeName(ctx context.Context) (string, error) {
	gitDir, err := r.Run(ctx, "rev-parse", "--git-dir")
	if err != nil {
		return "", err
	}
	commonDir, err := r.Run(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	gitDir, err = filepath.Abs(filepath.Join(r.root, gitDir))
	if err != nil {
		return "", fmt.Errorf("resolve git dir: %w", err)
	}
	commonDir, err = filepath.Abs(filepath.Join(r.root, commonDir))
	if err != nil {
		return "", fmt.Errorf("resolve git common dir: %w", err)
	}
	if gitDir == commonDir {
		return "", nil
	}
	return filepath.Base(gitDir), nil
}

// RemoteParts splits an SSH or HTTP git remote into its host and project path.
func RemoteParts(remote string) (host, project string) {
	value := strings.TrimSpace(remote)
	if value == "" {
		return "", ""
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", ""
		}
		return parsed.Hostname(), strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/")
	}
	host, project, ok := strings.Cut(value, ":")
	if !ok {
		return "", ""
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	return host, strings.Trim(strings.TrimSuffix(project, ".git"), "/")
}
