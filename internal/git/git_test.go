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
		Root:       "/repo",
		Cwd:        "/repo",
	}
	result := repoContext.Map()
	if result["branch"] != nil {
		t.Fatalf("Map() branch = %v, want nil for a detached HEAD", result["branch"])
	}
	if result["detached"] != true {
		t.Fatalf("Map() detached = %v, want true", result["detached"])
	}
	worktree, _ := result["worktree"].(map[string]any)
	if worktree["cwdIsRoot"] != true {
		t.Fatalf("Map() worktree = %#v, want cwdIsRoot", worktree)
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
	if repo.Root() != worktreeRoot || context.Root != worktreeRoot || context.Cwd != worktreeRoot {
		t.Fatalf("selected worktree = repo:%q context:%#v, want %q", repo.Root(), context, worktreeRoot)
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
