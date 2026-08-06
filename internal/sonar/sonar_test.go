package sonar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const projectKey = "acme:example"

func TestCompactIssue(t *testing.T) {
	actual := CompactIssue(map[string]any{
		"severity":  "CRITICAL",
		"rule":      "go:S123",
		"component": "app/main.go",
		"message":   "Fix this",
		"textRange": map[string]any{"startLine": float64(4), "endLine": float64(6)},
	})
	expected := map[string]any{
		"severity":  "CRITICAL",
		"rule":      "go:S123",
		"component": "app/main.go",
		"message":   "Fix this",
		"lineRange": []any{float64(4), float64(6)},
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("CompactIssue() = %#v, want %#v", actual, expected)
	}
}

func TestBranchName(t *testing.T) {
	client, err := New(Options{BaseURL: "https://sonar.example", ProjectKey: projectKey})
	if err != nil {
		t.Fatal(err)
	}
	if actual := client.BranchName("feature/test"); actual != "origin/feature/test" {
		t.Fatalf("BranchName() = %q, want origin/feature/test", actual)
	}
	if actual := client.BranchName("origin/feature/test"); actual != "origin/feature/test" {
		t.Fatalf("BranchName() = %q, want the branch unchanged", actual)
	}

	unprefixed, err := New(Options{BaseURL: "https://sonar.example", ProjectKey: projectKey, BranchPrefix: "-"})
	if err != nil {
		t.Fatal(err)
	}
	if actual := unprefixed.BranchName("main"); actual != "main" {
		t.Fatalf("BranchName() = %q, want main", actual)
	}
}

func TestIssuesReportsActionableCoverage(t *testing.T) {
	const branch = "origin/test"
	sonar := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/project_branches/list":
			write(writer, map[string]any{"branches": []any{map[string]any{"name": branch}}})
		case "/api/qualitygates/project_status":
			write(writer, map[string]any{"projectStatus": map[string]any{"conditions": []any{map[string]any{
				"status": "ERROR", "metricKey": "new_branch_coverage", "comparator": "LT", "errorThreshold": "70", "actualValue": "50.0",
			}}}})
		case "/api/measures/component_tree":
			write(writer, map[string]any{
				"components": []any{map[string]any{
					"key": projectKey + ":src/Example.java", "path": "src/Example.java",
					"measures": []any{map[string]any{"metric": "new_uncovered_conditions", "period": map[string]any{"value": "1"}}},
				}},
				"paging": map[string]any{"total": 1},
			})
		case "/api/sources/lines":
			write(writer, map[string]any{"sources": []any{map[string]any{
				"line": 42, "code": "  <span class=\"k\">if</span> (enabled) {", "isNew": true, "lineHits": 1, "conditions": 2, "coveredConditions": 1,
			}}})
		case "/api/issues/search":
			write(writer, map[string]any{"issues": []any{}, "paging": map[string]any{"total": 0}})
		default:
			http.Error(writer, "unexpected endpoint", http.StatusNotFound)
		}
	}))
	defer sonar.Close()

	client, err := New(Options{BaseURL: sonar.URL, Token: "token", ProjectKey: projectKey, HTTPClient: sonar.Client()})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := client.Issues(context.Background(), branch)
	if err != nil {
		t.Fatal(err)
	}

	expected := map[string]any{
		"failedConditions": []any{map[string]any{
			"status": "ERROR", "metricKey": "new_branch_coverage", "comparator": "LT", "errorThreshold": "70", "actualValue": "50.0",
		}},
		"coverageFiles": []any{map[string]any{
			"path":           "src/Example.java",
			"uncoveredLines": []any{},
			"partiallyCoveredLines": []any{map[string]any{
				"line": float64(42), "conditions": float64(2), "coveredConditions": float64(1), "code": "if (enabled) {",
			}},
		}},
		"issues": []any{},
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Issues() = %#v, want %#v", actual, expected)
	}
}

func TestIssuesRejectsUnknownBranches(t *testing.T) {
	sonar := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		write(writer, map[string]any{"branches": []any{map[string]any{"name": "origin/main"}}})
	}))
	defer sonar.Close()

	client, err := New(Options{BaseURL: sonar.URL, Token: "token", ProjectKey: projectKey, HTTPClient: sonar.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Issues(context.Background(), "missing"); err == nil {
		t.Fatal("Issues() accepted a branch SonarQube does not analyse")
	}
}

func write(writer http.ResponseWriter, value any) {
	_ = json.NewEncoder(writer).Encode(value)
}
