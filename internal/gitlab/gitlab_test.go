package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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
	actual := CompactReview(map[string]any{
		"id":     float64(3),
		"body":   "Please rename this",
		"author": map[string]any{"username": "reviewer"},
		"position": map[string]any{
			"new_path":   "internal/app.go",
			"line_range": map[string]any{"end": map[string]any{"new_line": float64(12)}},
		},
	})
	review, _ := actual.(map[string]any)
	if review["author"] != "reviewer" || review["path"] != "internal/app.go" || review["line"] != float64(12) {
		t.Fatalf("CompactReview() = %#v", review)
	}
	if CompactReview(nil) != nil {
		t.Fatal("CompactReview(nil) should be nil")
	}
}

func TestLatestReviewPrefersPositionedUnresolvedNotes(t *testing.T) {
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
	latest := client.latestReview(discussions, "me")
	if latest["body"] != "inline comment" {
		t.Fatalf("latestReview() = %#v, want the positioned note", latest)
	}
}

func TestReviewContextAllowsNoMergeRequest(t *testing.T) {
	requests := 0
	client := testClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		write(writer, []any{})
	})

	actual, err := client.ReviewContext(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("ReviewContext() made %d requests, want only the merge request lookup", requests)
	}
	if actual["merge_request"] != nil || actual["review"] != nil {
		t.Fatalf("ReviewContext() = %#v, want nil merge request and review", actual)
	}
}

func TestReviewContextIncludesAvailableMergeRequestReview(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/merge_requests"):
			write(writer, []any{map[string]any{"iid": float64(7), "title": "Add feature", "state": "opened"}})
		case request.URL.Path == "/user":
			write(writer, map[string]any{"username": "me"})
		default:
			write(writer, []any{map[string]any{
				"notes": []any{map[string]any{
					"id": float64(3), "body": "Please fix this", "created_at": "2026-01-01",
					"author": map[string]any{"username": "reviewer"},
				}},
			}})
		}
	})

	actual, err := client.ReviewContext(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	mergeRequest, _ := actual["merge_request"].(map[string]any)
	if mergeRequest["iid"] != float64(7) {
		t.Fatalf("ReviewContext() merge request = %#v", mergeRequest)
	}
	review, _ := actual["review"].(map[string]any)
	if review["body"] != "Please fix this" {
		t.Fatalf("ReviewContext() review = %#v", review)
	}
}

func TestReviewContextAllowsDetachedHead(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Fatal("ReviewContext() queried GitLab for a detached HEAD")
	})

	actual, err := client.ReviewContext(context.Background(), git.Context{Project: repo.Project, Commit: repo.Commit})
	if err != nil {
		t.Fatal(err)
	}
	if actual["merge_request"] != nil || actual["review"] != nil {
		t.Fatalf("ReviewContext() = %#v, want nil MR data", actual)
	}
}

func TestMergeRequestLookupPrefersOpenMergeRequests(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		write(writer, []any{
			map[string]any{"iid": float64(1), "state": "closed", "updated_at": "2026-01-09"},
			map[string]any{"iid": float64(2), "state": "opened", "updated_at": "2026-01-02"},
		})
	})

	selected, lookup, err := client.MergeRequestLookup(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	merged, _ := selected.(map[string]any)
	if merged["iid"] != float64(2) {
		t.Fatalf("MergeRequestLookup() selected %#v, want the open merge request", merged)
	}
	if lookup["reason"] != "open_merge_request" || lookup["open_matches"] != 1 || lookup["all_matches"] != 2 {
		t.Fatalf("MergeRequestLookup() lookup = %#v", lookup)
	}
	related, _ := lookup["related_merge_requests"].([]any)
	if len(related) != 2 {
		t.Fatalf("MergeRequestLookup() related = %#v, want both candidates", related)
	}
}

func TestMergeRequestLookupOnDetachedHead(t *testing.T) {
	client := testClient(t, nil)
	selected, lookup, err := client.MergeRequestLookup(context.Background(), git.Context{Project: "acme/example"})
	if err != nil {
		t.Fatal(err)
	}
	if selected != nil {
		t.Fatalf("MergeRequestLookup() selected %#v on a detached HEAD", selected)
	}
	if lookup["reason"] != "detached_head" {
		t.Fatalf("MergeRequestLookup() lookup = %#v, want a detached_head reason", lookup)
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

func TestCommitStatusSelectsNamedCurrentStatus(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Has("all") {
			t.Fatal("CommitStatus() requested superseded statuses")
		}
		write(writer, []any{
			map[string]any{"name": "lint", "target_url": "https://ci.example/lint", "created_at": "2026-01-09"},
			map[string]any{"name": "build", "target_url": "https://ci.example/2", "created_at": "2026-01-05", "status": "success", "pipeline": map[string]any{"id": float64(21)}, "author": map[string]any{"username": "jenkins"}},
		})
	})

	targetURL, status, err := client.CommitStatus(context.Background(), "acme/example", "abc123", "build")
	if err != nil {
		t.Fatal(err)
	}
	if targetURL != "https://ci.example/2" {
		t.Fatalf("CommitStatus() = %q, want the current build status", targetURL)
	}
	expected := map[string]any{"name": "build", "status": "success", "target_url": "https://ci.example/2", "created_at": "2026-01-05", "sha": "abc123", "pipeline_id": float64(21), "author": "jenkins"}
	if !reflect.DeepEqual(status, expected) {
		t.Fatalf("CommitStatus() status = %#v, want %#v", status, expected)
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

func TestCommitStatusWithoutMatchingStatus(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		write(writer, []any{})
	})
	if _, _, err := client.CommitStatus(context.Background(), "acme/example", "abc123", "build"); err == nil {
		t.Fatal("CommitStatus() accepted a commit without a build status")
	}
}

func TestCommitStatusRejectsUnusableCurrentStatus(t *testing.T) {
	for name, statuses := range map[string][]any{
		"missing target URL": {map[string]any{"name": "build", "status": "failed"}},
		"ambiguous": {
			map[string]any{"name": "build", "target_url": "https://ci.example/1"},
			map[string]any{"name": "build", "target_url": "https://ci.example/2"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := testClient(t, func(writer http.ResponseWriter, _ *http.Request) { write(writer, statuses) })
			if _, _, err := client.CommitStatus(context.Background(), "acme/example", "abc123", "build"); err == nil {
				t.Fatalf("CommitStatus() accepted %s", name)
			}
		})
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
