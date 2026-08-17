package jenkins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestNormalizeBuildURL(t *testing.T) {
	for _, test := range []struct {
		value    string
		expected string
	}{
		{value: "https://jenkins.example/job/build/42/display/redirect", expected: "https://jenkins.example/job/build/42/"},
		{value: "https://jenkins.example/job/build/42/consoleText", expected: "https://jenkins.example/job/build/42/"},
		{value: "https://jenkins.example/job/build/42?x=1", expected: "https://jenkins.example/job/build/42/"},
	} {
		actual, err := NormalizeBuildURL(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if actual != test.expected {
			t.Fatalf("NormalizeBuildURL(%q) = %q, want %q", test.value, actual, test.expected)
		}
	}

	for _, value := range []string{"", "not a url", "ftp://jenkins.example/job/1", "https://jenkins.example"} {
		if _, err := NormalizeBuildURL(value); err == nil {
			t.Fatalf("NormalizeBuildURL(%q) accepted an invalid URL", value)
		}
	}
}

func TestRemovedReport(t *testing.T) {
	const buildURL = "https://jenkins.example/job/build/42/"
	expected := map[string]any{
		"build": map[string]any{
			"result":          "REMOVED",
			"reportAvailable": false,
			"url":             buildURL,
		},
		"issues": []any{map[string]any{"kind": "build", "message": removedReportMessage}},
	}
	if actual := RemovedReport(buildURL); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("RemovedReport() = %#v, want %#v", actual, expected)
	}
}

func TestBuildReportCollectsStageAndTestFailures(t *testing.T) {
	jenkins := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/job/build/42/api/json":
			write(writer, map[string]any{"number": 42, "result": "FAILURE", "building": false, "url": "https://jenkins.example/job/build/42/"})
		case "/job/build/42/wfapi/describe":
			write(writer, map[string]any{"stages": []any{
				map[string]any{"name": "Compile", "status": "SUCCESS"},
				map[string]any{"name": "Test", "status": "FAILED", "error": map[string]any{"message": "tests failed"}},
				map[string]any{"name": "Deploy", "status": "NOT_EXECUTED"},
			}})
		case "/job/build/42/testReport/api/json":
			write(writer, map[string]any{"passCount": 1, "failCount": 1, "suites": []any{
				map[string]any{"name": "suite", "cases": []any{
					map[string]any{"name": "ok", "className": "ExampleTest", "status": "PASSED"},
					map[string]any{"name": "broken", "className": "ExampleTest", "status": "FAILED", "errorDetails": "expected <1> but was <2>"},
				}},
			}})
		case "/job/build/42/consoleText":
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = writer.Write([]byte("Tests run: 2, Failures: 1\nBUILD FAILURE\nunrelated line\n"))
		default:
			http.Error(writer, "unexpected endpoint", http.StatusNotFound)
		}
	}))
	defer jenkins.Close()

	client, err := New(Options{BaseURL: jenkins.URL, HTTPClient: jenkins.Client()})
	if err != nil {
		t.Fatal(err)
	}
	report, err := client.BuildReport(context.Background(), jenkins.URL+"/job/build/42/display/redirect")
	if err != nil {
		t.Fatal(err)
	}

	build, _ := report["build"].(map[string]any)
	if build["result"] != "FAILURE" {
		t.Fatalf("BuildReport() build = %#v, want a FAILURE result", build)
	}
	if _, ok := report["stages"]; ok {
		t.Fatalf("BuildReport() duplicated stages outside issues: %#v", report["stages"])
	}
	if build["pipelineId"] != nil || build["building"] != nil {
		t.Fatalf("BuildReport() kept duplicate build state: %#v", build)
	}

	issues, _ := report["issues"].([]map[string]any)
	kinds := make([]string, 0, len(issues))
	for _, issue := range issues {
		kinds = append(kinds, issue["kind"].(string))
	}
	expected := []string{"stage", "stage", "test", "log", "log"}
	if !reflect.DeepEqual(kinds, expected) {
		t.Fatalf("BuildReport() issue kinds = %#v, want %#v", kinds, expected)
	}
	if issues[0]["name"] != "Test" || issues[1]["name"] != "Deploy" {
		t.Fatalf("BuildReport() stage issues = %#v", issues[:2])
	}
}

func TestBuildReportReportsRemovedBuilds(t *testing.T) {
	jenkins := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "gone", http.StatusNotFound)
	}))
	defer jenkins.Close()

	client, err := New(Options{BaseURL: jenkins.URL, HTTPClient: jenkins.Client()})
	if err != nil {
		t.Fatal(err)
	}
	report, err := client.BuildReport(context.Background(), jenkins.URL+"/job/build/42/")
	if err != nil {
		t.Fatal(err)
	}
	build, _ := report["build"].(map[string]any)
	if build["result"] != "REMOVED" {
		t.Fatalf("BuildReport() = %#v, want a REMOVED result", report)
	}
}

func TestBuildReportRejectsForeignBuildURL(t *testing.T) {
	jenkins := httptest.NewServer(http.NotFoundHandler())
	defer jenkins.Close()
	foreign := httptest.NewServer(http.NotFoundHandler())
	defer foreign.Close()

	client, err := New(Options{BaseURL: jenkins.URL, HTTPClient: jenkins.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.BuildReport(context.Background(), foreign.URL+"/job/build/42/"); err == nil {
		t.Fatal("BuildReport() accepted a build URL outside Jenkins")
	}
}

func write(writer http.ResponseWriter, value any) {
	_ = json.NewEncoder(writer).Encode(value)
}
