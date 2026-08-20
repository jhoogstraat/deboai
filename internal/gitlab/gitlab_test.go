package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jhoogstraat/deboai/internal/git"
)

var repo = git.Context{Project: "acme/example", RemoteHost: "gitlab.example", Branch: "feature/test", Commit: "abc123"}

func TestCompactMergeRequest(t *testing.T) {
	actual := CompactMergeRequest(map[string]any{
		"iid": float64(7), "title": "Add feature", "state": "opened", "ignored": true,
	})
	if actual["iid"] != float64(7) || actual["title"] != "Add feature" {
		t.Fatalf("CompactMergeRequest() = %#v", actual)
	}
	if _, ok := actual["ignored"]; ok {
		t.Fatalf("CompactMergeRequest() kept an unexpected field: %#v", actual)
	}
}

func TestCompactReviewFlattensPosition(t *testing.T) {
	review := CompactReview(map[string]any{
		"id":     float64(3),
		"body":   "Please rename this",
		"author": map[string]any{"username": "reviewer"},
		"position": map[string]any{
			"new_path":   "internal/app.go",
			"line_range": map[string]any{"end": map[string]any{"new_line": float64(12)}},
		},
	})
	if review["author"] != "reviewer" || review["path"] != "internal/app.go" || review["line"] != float64(12) {
		t.Fatalf("CompactReview() = %#v", review)
	}
	if CompactReview(nil) != nil {
		t.Fatal("CompactReview(nil) should be nil")
	}
}

func TestActionableNotesKeepsUnresolvedNotesOldestFirst(t *testing.T) {
	client := testClient(t, nil)
	client.ignoredAuthors = []string{"ci-bot"}
	discussions := []map[string]any{
		{"resolved": true, "notes": []any{map[string]any{"body": "resolved", "created_at": "2026-01-05"}}},
		{"notes": []any{
			map[string]any{"body": "system note", "system": true, "created_at": "2026-01-04"},
			map[string]any{"body": "pipeline failed", "author": map[string]any{"username": "ci-bot"}, "created_at": "2026-01-03"},
			map[string]any{"body": "mine", "author": map[string]any{"username": "me"}, "created_at": "2026-01-02"},
			map[string]any{"body": "general comment", "author": map[string]any{"username": "reviewer"}, "created_at": "2026-01-06"},
			map[string]any{"body": "inline comment", "author": map[string]any{"username": "reviewer"}, "created_at": "2026-01-01",
				"position": map[string]any{"new_path": "app.go"}},
		}},
	}
	notes := client.actionableNotes(discussions, "me")
	if len(notes) != 2 || notes[0]["body"] != "inline comment" || notes[1]["body"] != "general comment" {
		t.Fatalf("actionableNotes() = %#v, want the actionable notes oldest first", notes)
	}
}

func TestOpenChangeCompactsTheOpenMergeRequest(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		write(writer, []any{map[string]any{"iid": float64(7), "title": "Add feature", "state": "opened", "ignored": true}})
	})

	change, err := client.OpenChange(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if change["iid"] != float64(7) || change["title"] != "Add feature" {
		t.Fatalf("OpenChange() = %#v", change)
	}
	if _, ok := change["ignored"]; ok {
		t.Fatalf("OpenChange() kept an unexpected field: %#v", change)
	}
}

func TestOpenChangeReturnsNilWithoutOpenMergeRequest(t *testing.T) {
	client := testClient(t, nil)
	change, err := client.OpenChange(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		t.Fatalf("OpenChange() = %#v, want nil", change)
	}
}

func TestReviewsCompactsTheActionableNotes(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/user" {
			write(writer, map[string]any{"username": "me"})
			return
		}
		write(writer, []any{map[string]any{
			"notes": []any{
				map[string]any{
					"id": float64(3), "body": "Please fix this", "created_at": "2026-01-01",
					"author": map[string]any{"username": "reviewer"},
				},
				map[string]any{
					"id": float64(4), "body": "And this too", "created_at": "2026-01-02",
					"author": map[string]any{"username": "reviewer"},
				},
			},
		}})
	})

	reviews, err := client.Reviews(context.Background(), repo, map[string]any{"iid": float64(7)})
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 2 || reviews[0]["body"] != "Please fix this" || reviews[1]["body"] != "And this too" {
		t.Fatalf("Reviews() = %#v, want both actionable notes", reviews)
	}
	if reviews[0]["author"] != "reviewer" {
		t.Fatalf("Reviews() = %#v", reviews)
	}
}

func TestProjectPrefersTheConfiguredProject(t *testing.T) {
	client := testClient(t, nil)
	project, err := client.Project(repo)
	if err != nil {
		t.Fatal(err)
	}
	if project != "acme/example" {
		t.Fatalf("Project() = %q, want the origin remote project", project)
	}

	client.project = "42"
	if project, err = client.Project(repo); err != nil || project != "42" {
		t.Fatalf("Project() = %q, %v, want the configured project", project, err)
	}

	client.project = ""
	if _, err = client.Project(git.Context{}); err == nil {
		t.Fatal("Project() accepted a repository without a project")
	}
}

func TestOpenMergeRequestReturnsNilOnDetachedHead(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Fatal("OpenMergeRequest() queried GitLab for a detached HEAD")
	})
	mergeRequest, err := client.OpenMergeRequest(context.Background(), git.Context{Project: "acme/example"})
	if err != nil {
		t.Fatal(err)
	}
	if mergeRequest != nil {
		t.Fatalf("OpenMergeRequest() = %#v on a detached HEAD, want nil", mergeRequest)
	}
}

func TestOpenMergeRequestIgnoresClosedAndMergedRequests(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("state") != "opened" {
			t.Fatalf("OpenMergeRequest() queried state=%q, want opened", request.URL.Query().Get("state"))
		}
		write(writer, []any{})
	})
	mergeRequest, err := client.OpenMergeRequest(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if mergeRequest != nil {
		t.Fatalf("OpenMergeRequest() = %#v, want nil when only closed/merged requests exist", mergeRequest)
	}
}

func TestOpenMergeRequestRejectsAmbiguousBranches(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		write(writer, []any{
			map[string]any{"iid": float64(1), "state": "opened"},
			map[string]any{"iid": float64(2), "state": "opened"},
		})
	})
	if _, err := client.OpenMergeRequest(context.Background(), repo); err == nil {
		t.Fatal("OpenMergeRequest() accepted two open merge requests")
	}
}

func TestCommitStatusesCompactsGateProvenance(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Has("all") {
			t.Fatal("CommitStatuses() requested superseded statuses")
		}
		write(writer, []any{
			map[string]any{"name": "build", "target_url": "https://ci.example/2", "created_at": "2026-01-05", "status": "success", "pipeline": map[string]any{"id": float64(21)}, "author": map[string]any{"username": "jenkins"}},
		})
	})

	statuses, err := client.CommitStatuses(context.Background(), "acme/example", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{"name": "build", "status": "success", "target_url": "https://ci.example/2", "created_at": "2026-01-05", "sha": "abc123", "pipeline_id": float64(21), "author": "jenkins"}
	if len(statuses) != 1 || !reflect.DeepEqual(statuses[0], expected) {
		t.Fatalf("CommitStatuses() = %#v, want %#v", statuses, expected)
	}
}

func TestCommitStatusesRequestsOnlyLatestGateStates(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Has("all") {
			t.Fatal("CommitStatuses() requested superseded statuses")
		}
		write(writer, []any{
			map[string]any{"name": "build", "status": "success", "target_url": "https://ci.example/build"},
			map[string]any{"name": "sonar", "status": "failed", "target_url": "https://ci.example/sonar"},
		})
	})

	statuses, err := client.CommitStatuses(context.Background(), "acme/example", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("CommitStatuses() returned %d statuses, want one per gate", len(statuses))
	}
}

func TestCommitStatusesPaginates(t *testing.T) {
	requests := 0
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		requests++
		count := pageSize
		if request.URL.Query().Get("page") == "2" {
			count = 1
		}
		statuses := make([]any, count)
		for index := range statuses {
			statuses[index] = map[string]any{"name": fmt.Sprintf("gate-%d", index), "status": "success"}
		}
		write(writer, statuses)
	})

	statuses, err := client.CommitStatuses(context.Background(), "acme/example", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != pageSize+1 || requests != 2 {
		t.Fatalf("CommitStatuses() returned %d statuses in %d requests", len(statuses), requests)
	}
}

func TestCommitStatusesFollowsGitLabPaginationHeader(t *testing.T) {
	requests := 0
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Query().Get("page") == "1" {
			writer.Header().Set("X-Next-Page", "2")
		}
		write(writer, []any{map[string]any{"name": fmt.Sprintf("gate-%d", requests), "status": "success"}})
	})

	statuses, err := client.CommitStatuses(context.Background(), "acme/example", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 || requests != 2 {
		t.Fatalf("CommitStatuses() returned %d statuses in %d requests", len(statuses), requests)
	}
}

func TestProjectPath(t *testing.T) {
	if actual := ProjectPath("acme/example"); actual != "acme%2Fexample" {
		t.Fatalf("ProjectPath() = %q", actual)
	}
	if actual := ProjectPath(""); actual != ":fullpath" {
		t.Fatalf("ProjectPath(\"\") = %q", actual)
	}
}

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	if handler == nil {
		handler = func(writer http.ResponseWriter, _ *http.Request) { write(writer, []any{}) }
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := New(Options{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func write(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
