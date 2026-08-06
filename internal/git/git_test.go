package git

import "testing"

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
