package vcs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/git"
	"github.com/jhoogstraat/deboai/internal/github"
	"github.com/jhoogstraat/deboai/internal/gitlab"
)

var repo = git.Context{Project: "acme/example", RemoteHost: "gitlab.example", Branch: "feature/test", Commit: "abc123"}

func TestFromValuesSelectsGitLabForGitLabHosts(t *testing.T) {
	values := config.Values{"GITLAB_API_URL": "https://gitlab.example/api/v4", "GITLAB_TOKEN": "token"}
	provider, err := FromValues(values, "gitlab.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*gitlab.Client); !ok {
		t.Fatalf("FromValues() = %T, want *gitlab.Client", provider)
	}
}

func TestFromValuesSelectsGitLabForConfiguredUnknownHosts(t *testing.T) {
	values := config.Values{"GITLAB_API_URL": "https://git.example/api/v4", "GITLAB_TOKEN": "token"}
	provider, err := FromValues(values, "git.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*gitlab.Client); !ok {
		t.Fatalf("FromValues() = %T, want *gitlab.Client", provider)
	}
}

func TestFromValuesSelectsGitHubForGitHubHosts(t *testing.T) {
	provider, err := FromValues(config.Values{"GITHUB_TOKEN": "token"}, "github.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*github.Client); !ok {
		t.Fatalf("FromValues() = %T, want *github.Client", provider)
	}
}

func TestFromValuesSelectsGitHubForConfiguredUnknownHosts(t *testing.T) {
	values := config.Values{"GITHUB_API_URL": "https://ghe.example/api/v3", "GITHUB_TOKEN": "token"}
	provider, err := FromValues(values, "ghe.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*github.Client); !ok {
		t.Fatalf("FromValues() = %T, want *github.Client", provider)
	}
}

func TestFromValuesPrefersTheNamedHostOverConfiguration(t *testing.T) {
	values := config.Values{
		"GITHUB_API_URL": "https://ghe.example/api/v3", "GITHUB_TOKEN": "token",
		"GITLAB_API_URL": "https://gitlab.example/api/v4", "GITLAB_TOKEN": "token",
	}
	provider, err := FromValues(values, "gitlab.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*gitlab.Client); !ok {
		t.Fatalf("FromValues() = %T, want *gitlab.Client", provider)
	}
}

func TestFromValuesRejectsUnsupportedHosts(t *testing.T) {
	_, err := FromValues(config.Values{}, "bitbucket.org")
	if err == nil || !strings.Contains(err.Error(), `unsupported VCS host "bitbucket.org"`) {
		t.Fatalf("FromValues() error = %v, want an unsupported host error naming the host", err)
	}
}

func TestFromValuesRejectsMissingHostWithoutConfiguration(t *testing.T) {
	if _, err := FromValues(config.Values{}, ""); err == nil {
		t.Fatal("FromValues() accepted an empty host without a configured backend")
	}
}

func TestReviewContextAllowsNoMergeRequest(t *testing.T) {
	requests := 0
	provider := testProvider(t, func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		write(writer, []any{})
	})

	actual, err := ReviewContext(context.Background(), provider, repo)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("ReviewContext() made %d requests, want only the merge request lookup", requests)
	}
	if actual["mr"] != nil || actual["review"] != nil {
		t.Fatalf("ReviewContext() = %#v, want nil merge request and review", actual)
	}
}

func TestReviewContextIncludesAvailableMergeRequestReview(t *testing.T) {
	provider := testProvider(t, func(writer http.ResponseWriter, request *http.Request) {
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

	actual, err := ReviewContext(context.Background(), provider, repo)
	if err != nil {
		t.Fatal(err)
	}
	mergeRequest, _ := actual["mr"].(map[string]any)
	if mergeRequest["iid"] != float64(7) {
		t.Fatalf("ReviewContext() merge request = %#v", mergeRequest)
	}
	review, _ := actual["review"].(map[string]any)
	if review["body"] != "Please fix this" {
		t.Fatalf("ReviewContext() review = %#v", review)
	}
}

func TestReviewContextAllowsDetachedHead(t *testing.T) {
	provider := testProvider(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Fatal("ReviewContext() queried the provider for a detached HEAD")
	})

	actual, err := ReviewContext(context.Background(), provider, git.Context{Project: repo.Project, Commit: repo.Commit})
	if err != nil {
		t.Fatal(err)
	}
	if actual["mr"] != nil || actual["review"] != nil {
		t.Fatalf("ReviewContext() = %#v, want nil MR data", actual)
	}
}

func TestCommitStatusSelectsNamedCurrentStatus(t *testing.T) {
	provider := testProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		write(writer, []any{
			map[string]any{"name": "lint", "target_url": "https://ci.example/lint", "created_at": "2026-01-09"},
			map[string]any{"name": "build", "target_url": "https://ci.example/2", "created_at": "2026-01-05", "status": "success"},
		})
	})

	targetURL, status, err := CommitStatus(context.Background(), provider, "acme/example", "abc123", "build")
	if err != nil {
		t.Fatal(err)
	}
	if targetURL != "https://ci.example/2" {
		t.Fatalf("CommitStatus() = %q, want the current build status", targetURL)
	}
	if status["status"] != "success" {
		t.Fatalf("CommitStatus() status = %#v", status)
	}
}

func TestCommitStatusWithoutMatchingStatus(t *testing.T) {
	provider := testProvider(t, func(writer http.ResponseWriter, _ *http.Request) {
		write(writer, []any{})
	})
	if _, _, err := CommitStatus(context.Background(), provider, "acme/example", "abc123", "build"); err == nil {
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
			provider := testProvider(t, func(writer http.ResponseWriter, _ *http.Request) { write(writer, statuses) })
			if _, _, err := CommitStatus(context.Background(), provider, "acme/example", "abc123", "build"); err == nil {
				t.Fatalf("CommitStatus() accepted %s", name)
			}
		})
	}
}

func TestSelectCommitPrefersMergeRequestHeadSHA(t *testing.T) {
	provider := testProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/merge_requests"):
			write(writer, []any{map[string]any{"iid": float64(7), "state": "opened", "sha": "mr-head"}})
		default:
			t.Fatalf("unexpected request: %s", request.URL.Path)
		}
	})

	selection, err := SelectCommit(context.Background(), provider, repo)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Project != "acme/example" || selection.Commit != "mr-head" || selection.ChangeID != float64(7) {
		t.Fatalf("SelectCommit() = %#v", selection)
	}
}

func TestSelectCommitFallsBackToCheckoutWithoutMergeRequest(t *testing.T) {
	provider := testProvider(t, nil)
	selection, err := SelectCommit(context.Background(), provider, repo)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Commit != repo.Commit || selection.ChangeID != nil {
		t.Fatalf("SelectCommit() = %#v", selection)
	}
}

func testProvider(t *testing.T, handler http.HandlerFunc) Provider {
	t.Helper()
	if handler == nil {
		handler = func(writer http.ResponseWriter, _ *http.Request) { write(writer, []any{}) }
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	provider, err := gitlab.New(gitlab.Options{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func write(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
