package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jhoogstraat/deboai/internal/git"
)

var repo = git.Context{Project: "acme/example", RemoteHost: "github.com", Branch: "feature/test", Commit: "abc123"}

func TestCompactPullRequest(t *testing.T) {
	actual := CompactPullRequest(map[string]any{
		"number": float64(7), "title": "Add feature", "state": "open", "draft": false,
		"head":     map[string]any{"ref": "feature/test", "sha": "pr-head"},
		"base":     map[string]any{"ref": "main"},
		"html_url": "https://github.example/acme/example/pull/7",
		"ignored":  true,
	})
	expected := map[string]any{
		"iid": float64(7), "title": "Add feature", "state": "open", "draft": false,
		"source_branch": "feature/test", "target_branch": "main", "sha": "pr-head",
		"web_url": "https://github.example/acme/example/pull/7",
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("CompactPullRequest() = %#v, want %#v", actual, expected)
	}
}

func TestCompactReviewCommentKeepsDiffPosition(t *testing.T) {
	actual := CompactReviewComment(map[string]any{
		"id":         float64(3),
		"body":       "Please rename this",
		"user":       map[string]any{"login": "reviewer"},
		"path":       "internal/app.go",
		"line":       float64(12),
		"commit_id":  "pr-head",
		"created_at": "2026-01-01",
	})
	comment, _ := actual.(map[string]any)
	if comment["author"] != "reviewer" || comment["path"] != "internal/app.go" || comment["line"] != float64(12) || comment["head_sha"] != "pr-head" {
		t.Fatalf("CompactReviewComment() = %#v", comment)
	}
	if CompactReviewComment(nil) != nil {
		t.Fatal("CompactReviewComment(nil) should be nil")
	}
}

func TestProjectPrefersTheConfiguredRepo(t *testing.T) {
	client := testClient(t, nil)
	project, err := client.Project(repo)
	if err != nil {
		t.Fatal(err)
	}
	if project != "acme/example" {
		t.Fatalf("Project() = %q, want the origin remote project", project)
	}

	client.repo = "acme/other"
	if project, err = client.Project(repo); err != nil || project != "acme/other" {
		t.Fatalf("Project() = %q, %v, want the configured repo", project, err)
	}

	client.repo = ""
	if _, err = client.Project(git.Context{Project: "not-owner-repo"}); err == nil {
		t.Fatal("Project() accepted a project without an owner")
	}
}

func TestOpenChangeCompactsTheOpenPullRequest(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/acme/example/pulls" {
			t.Fatalf("unexpected request: %s", request.URL.Path)
		}
		if request.URL.Query().Get("head") != "acme:feature/test" || request.URL.Query().Get("state") != "open" {
			t.Fatalf("OpenChange() queried %s", request.URL.RawQuery)
		}
		write(writer, []any{map[string]any{
			"number": float64(7), "state": "open", "head": map[string]any{"sha": "pr-head"},
		}})
	})

	change, err := client.OpenChange(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if change["iid"] != float64(7) || change["sha"] != "pr-head" {
		t.Fatalf("OpenChange() = %#v", change)
	}
}

func TestOpenChangeReturnsNilOnDetachedHead(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Fatal("OpenChange() queried GitHub for a detached HEAD")
	})
	change, err := client.OpenChange(context.Background(), git.Context{Project: repo.Project, Commit: repo.Commit})
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		t.Fatalf("OpenChange() = %#v on a detached HEAD, want nil", change)
	}
}

func TestOpenChangeRejectsAmbiguousBranches(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		write(writer, []any{
			map[string]any{"number": float64(1), "state": "open"},
			map[string]any{"number": float64(2), "state": "open"},
		})
	})
	if _, err := client.OpenChange(context.Background(), repo); err == nil {
		t.Fatal("OpenChange() accepted two open pull requests")
	}
}

func TestLatestReviewPrefersPositionedComments(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user":
			write(writer, map[string]any{"login": "me"})
		case "/repos/acme/example/pulls/7/comments":
			write(writer, []any{
				map[string]any{"body": "mine", "user": map[string]any{"login": "me"}, "created_at": "2026-01-09", "path": "app.go", "line": float64(2)},
				map[string]any{"body": "pipeline failed", "user": map[string]any{"login": "ci-bot"}, "created_at": "2026-01-08", "path": "app.go"},
				map[string]any{"body": "inline comment", "user": map[string]any{"login": "reviewer"}, "created_at": "2026-01-01", "path": "app.go", "line": float64(12)},
			})
		case "/repos/acme/example/issues/7/comments":
			write(writer, []any{
				map[string]any{"body": "general comment", "user": map[string]any{"login": "reviewer"}, "created_at": "2026-01-06"},
			})
		default:
			t.Fatalf("unexpected request: %s", request.URL.Path)
		}
	})
	client.ignoredAuthors = []string{"ci-bot"}

	actual, err := client.LatestReview(context.Background(), repo, map[string]any{"iid": float64(7)})
	if err != nil {
		t.Fatal(err)
	}
	review, _ := actual.(map[string]any)
	if review["body"] != "inline comment" {
		t.Fatalf("LatestReview() = %#v, want the positioned comment", review)
	}
}

func TestLatestReviewFallsBackToIssueComments(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user":
			write(writer, map[string]any{"login": "me"})
		case "/repos/acme/example/pulls/7/comments":
			write(writer, []any{})
		case "/repos/acme/example/issues/7/comments":
			write(writer, []any{
				map[string]any{"body": "older", "user": map[string]any{"login": "reviewer"}, "created_at": "2026-01-01"},
				map[string]any{"body": "please split this PR", "user": map[string]any{"login": "reviewer"}, "created_at": "2026-01-06"},
			})
		default:
			t.Fatalf("unexpected request: %s", request.URL.Path)
		}
	})

	actual, err := client.LatestReview(context.Background(), repo, map[string]any{"iid": float64(7)})
	if err != nil {
		t.Fatal(err)
	}
	review, _ := actual.(map[string]any)
	if review["body"] != "please split this PR" || review["author"] != "reviewer" {
		t.Fatalf("LatestReview() = %#v, want the latest issue comment", review)
	}
	if _, ok := review["path"]; ok {
		t.Fatalf("LatestReview() = %#v, want no diff position on an issue comment", review)
	}
}

func TestLatestReviewReturnsNilWithoutActionableComments(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/user" {
			write(writer, map[string]any{"login": "me"})
			return
		}
		write(writer, []any{map[string]any{"body": "mine", "user": map[string]any{"login": "me"}, "created_at": "2026-01-01"}})
	})

	actual, err := client.LatestReview(context.Background(), repo, map[string]any{"iid": float64(7)})
	if err != nil {
		t.Fatal(err)
	}
	if actual != nil {
		t.Fatalf("LatestReview() = %#v, want nil", actual)
	}
}

func TestCommitStatusesMergesStatusesAndCheckRuns(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/acme/example/commits/abc123/status":
			write(writer, map[string]any{"state": "failure", "statuses": []any{
				map[string]any{
					"id": float64(1), "context": "jenkins/build", "state": "failure",
					"target_url": "https://jenkins.example/42", "created_at": "2026-01-05",
					"creator": map[string]any{"login": "jenkins"},
				},
			}})
		case "/repos/acme/example/commits/abc123/check-runs":
			write(writer, map[string]any{"total_count": float64(1), "check_runs": []any{
				map[string]any{
					"id": float64(2), "name": "tests", "status": "completed", "conclusion": "success",
					"details_url": "https://github.example/acme/example/runs/2",
					"head_sha":    "abc123", "started_at": "2026-01-05", "completed_at": "2026-01-06",
					"output": map[string]any{"title": "120 passed"},
				},
			}})
		default:
			t.Fatalf("unexpected request: %s", request.URL.Path)
		}
	})

	statuses, err := client.CommitStatuses(context.Background(), "acme/example", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	expected := []map[string]any{
		{
			"id": float64(1), "name": "jenkins/build", "status": "failed", "sha": "abc123",
			"target_url": "https://jenkins.example/42", "created_at": "2026-01-05", "author": "jenkins",
		},
		{
			"id": float64(2), "name": "tests", "status": "success", "sha": "abc123",
			"target_url":  "https://github.example/acme/example/runs/2",
			"description": "120 passed", "started_at": "2026-01-05", "finished_at": "2026-01-06",
		},
	}
	if !reflect.DeepEqual(statuses, expected) {
		t.Fatalf("CommitStatuses() = %#v, want %#v", statuses, expected)
	}
}

func TestCommitStatusesFollowsPaginationLinks(t *testing.T) {
	requests := 0
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if strings.HasSuffix(request.URL.Path, "/status") {
			if request.URL.Query().Get("page") == "1" {
				writer.Header().Set("Link", `<https://api.github.example/next>; rel="next"`)
			}
			write(writer, map[string]any{"statuses": []any{
				map[string]any{"context": "gate-" + request.URL.Query().Get("page"), "state": "success"},
			}})
			return
		}
		write(writer, map[string]any{"check_runs": []any{}})
	})

	statuses, err := client.CommitStatuses(context.Background(), "acme/example", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 || requests != 3 {
		t.Fatalf("CommitStatuses() returned %d statuses in %d requests", len(statuses), requests)
	}
}

func TestCheckRunStateNormalizesTheStatusVocabulary(t *testing.T) {
	for _, test := range []struct{ status, conclusion, expected string }{
		{"queued", "", "pending"},
		{"in_progress", "", "running"},
		{"completed", "success", "success"},
		{"completed", "failure", "failed"},
		{"completed", "timed_out", "failed"},
		{"completed", "cancelled", "canceled"},
		{"completed", "skipped", "skipped"},
		{"completed", "neutral", "skipped"},
		{"completed", "action_required", "manual"},
	} {
		if actual := checkRunState(test.status, test.conclusion); actual != test.expected {
			t.Fatalf("checkRunState(%q, %q) = %q, want %q", test.status, test.conclusion, actual, test.expected)
		}
	}
}

func TestNewRequiresAToken(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New() accepted empty options")
	}
	client, err := New(Options{Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if client.baseURL != DefaultBaseURL {
		t.Fatalf("New() baseURL = %q, want %q", client.baseURL, DefaultBaseURL)
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
