package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteParts(t *testing.T) {
	for _, test := range []struct {
		remote  string
		host    string
		project string
	}{
		{remote: "https://gitlab.example/acme/example.git", host: "gitlab.example", project: "acme/example"},
		{remote: "git@gitlab.example:acme/example.git", host: "gitlab.example", project: "acme/example"},
		{remote: "https://gitlab.example/acme/group/example", host: "gitlab.example", project: "acme/group/example"},
		{remote: "", host: "", project: ""},
	} {
		host, project := RemoteParts(test.remote)
		if host != test.host || project != test.project {
			t.Fatalf("RemoteParts(%q) = %q, %q; want %q, %q", test.remote, host, project, test.host, test.project)
		}
	}
}

func TestContextMap(t *testing.T) {
	repoContext := Context{
		Project:    "acme/example",
		RemoteHost: "gitlab.example",
		Commit:     "abc123",
	}
	result := repoContext.Map()
	if result["branch"] != nil {
		t.Fatalf("Map() branch = %v, want nil for a detached HEAD", result["branch"])
	}
	if _, ok := result["detached"]; ok {
		t.Fatalf("Map() result = %#v, want no detached key", result)
	}
	remote, _ := result["remote"].(map[string]any)
	if remote["project"] != "acme/example" {
		t.Fatalf("Map() remote = %#v, want project acme/example", remote)
	}
	if _, ok := result["project"]; ok {
		t.Fatalf("Map() result = %#v, want no top-level project key", result)
	}
	if _, ok := result["worktree"]; ok {
		t.Fatalf("Map() result = %#v, want no worktree key for the main worktree", result)
	}
}

func TestContextMapWorktree(t *testing.T) {
	repoContext := Context{Commit: "abc123", Worktree: "feature"}
	result := repoContext.Map()
	if result["worktree"] != "feature" {
		t.Fatalf("Map() worktree = %v, want feature", result["worktree"])
	}
}

func TestOpenWorktreeDefaultsToCurrentDirectory(t *testing.T) {
	root := testRepository(t)
	t.Setenv(RootVariable, "")
	t.Chdir(root)

	repo, err := OpenWorktree(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root() != root {
		t.Fatalf("OpenWorktree() root = %q, want %q", repo.Root(), root)
	}
}

func TestOpenWorktreeUsesConfiguredDirectory(t *testing.T) {
	root := testRepository(t)
	t.Setenv(RootVariable, root)

	repo, err := OpenWorktree(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root() != root {
		t.Fatalf("OpenWorktree() root = %q, want %q", repo.Root(), root)
	}
}

func TestOpenWorktreeRejectsMissingConfiguredDirectory(t *testing.T) {
	t.Setenv(RootVariable, filepath.Join(t.TempDir(), "missing"))
	if _, err := OpenWorktree(context.Background(), ""); err == nil {
		t.Fatal("OpenWorktree accepted a missing configured directory")
	}
}

func TestOpenWorktreeUsesSelectedLinkedWorktree(t *testing.T) {
	mainRoot := testRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "feature")
	runGit(t, mainRoot, "worktree", "add", "-b", "feature/worktree", worktreeRoot)
	var err error
	if worktreeRoot, err = filepath.EvalSymlinks(worktreeRoot); err != nil {
		t.Fatal(err)
	}

	nested := filepath.Join(worktreeRoot, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := OpenWorktree(context.Background(), nested)
	if err != nil {
		t.Fatal(err)
	}
	context, err := repo.Context(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root() != worktreeRoot {
		t.Fatalf("selected worktree = repo:%q, want %q", repo.Root(), worktreeRoot)
	}
	if context.Worktree != filepath.Base(worktreeRoot) {
		t.Fatalf("selected worktree name = %q, want %q", context.Worktree, filepath.Base(worktreeRoot))
	}
	if context.Branch != "feature/worktree" {
		t.Fatalf("selected branch = %q, want feature/worktree", context.Branch)
	}
}

func testRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "initial")
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}
