package jenkins

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jhoogstraat/deboai/internal/jsonutil"
	"github.com/jhoogstraat/deboai/internal/textutil"
)

const (
	maxIssueMessageLength = 1600
	maxHighlights         = 20
	maxHighlightLength    = 500
	maxDescriptionLength  = 500
)

// removedReportMessage is reported when Jenkins has discarded the build record.
const removedReportMessage = "Jenkins reports that this build report was removed. Rerun the pipeline to inspect stages and tests."

var consoleHighlightPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)Tests run:\s*\d+,\s*Failures:\s*[1-9]\d*`),
	regexp.MustCompile(`(?i)Tests run:\s*\d+,\s*Failures:\s*0,\s*Errors:\s*[1-9]\d*`),
	regexp.MustCompile(`(?i)Failed to execute goal`),
	regexp.MustCompile(`(?i)BUILD (?:FAILURE|UNSTABLE)`),
	regexp.MustCompile(`(?i)Finished:\s+(?:FAILURE|UNSTABLE|ABORTED)`),
	regexp.MustCompile(`(?i)Failed in branch`),
	regexp.MustCompile(`(?i)ERROR:\s+The build`),
}

// BuildReport fetches a build and reduces it to its actionable failures. The
// returned map holds the build summary plus, when the report still exists, the
// failed stages, the test summary, and the combined issue list.
func (c *Client) BuildReport(ctx context.Context, buildURL string) (map[string]any, error) {
	normalized, err := NormalizeBuildURL(buildURL)
	if err != nil {
		return nil, err
	}
	if !c.acceptsBuildURL(normalized) {
		return nil, fmt.Errorf("Jenkins build URL is outside JENKINS_URL: %s", buildURL)
	}
	client := c.forBuild(normalized)

	build, err := client.jsonRequest(ctx, "api/json", url.Values{
		"tree": {"number,result,building,timestamp,duration,url,displayName,fullDisplayName,description"},
	}, true)
	if err != nil {
		return nil, err
	}
	if build == nil {
		return RemovedReport(normalized), nil
	}

	workflow, err := client.jsonRequest(ctx, "wfapi/describe", nil, true)
	if err != nil {
		return nil, err
	}
	failedStages, notExecutedStages := stageIssues(workflow)

	report, err := client.jsonRequest(ctx, "testReport/api/json", url.Values{
		"tree": {"passCount,failCount,skipCount,suites[name,cases[name,className,status,errorDetails,errorStackTrace]]"},
	}, true)
	if err != nil {
		return nil, err
	}
	testSummary, testFailures := testReportIssues(report)

	highlights := []string{}
	result := jsonutil.String(build["result"])
	building, _ := build["building"].(bool)
	if building || (result != "" && result != "SUCCESS") {
		console, err := client.request(ctx, "consoleText", nil, "text/plain", true)
		if err != nil {
			return nil, err
		}
		highlights = consoleHighlights(string(console))
	}

	issues := make([]map[string]any, 0, len(failedStages)+len(notExecutedStages)+len(testFailures)+len(highlights))
	for _, stage := range failedStages {
		issues = append(issues, jsonutil.Merge(map[string]any{"kind": "stage"}, stage))
	}
	for _, stage := range notExecutedStages {
		issues = append(issues, jsonutil.Merge(map[string]any{"kind": "stage"}, stage))
	}
	for _, failure := range testFailures {
		issues = append(issues, jsonutil.Merge(map[string]any{"kind": "test"}, failure))
	}
	for _, highlight := range highlights {
		issues = append(issues, map[string]any{"kind": "log", "message": highlight})
	}

	return map[string]any{
		"build": map[string]any{
			"number":      build["number"],
			"result":      buildResult(build),
			"timestamp":   isoTimestamp(build["timestamp"]),
			"durationMs":  build["duration"],
			"url":         jsonutil.FirstNonEmpty(build["url"], normalized),
			"description": jsonutil.Nullable(textutil.Clean(jsonutil.String(build["description"]), maxDescriptionLength)),
		},
		"tests":  testSummary,
		"issues": issues,
	}, nil
}

// RemovedReport describes a build whose report Jenkins has discarded.
func RemovedReport(buildURL string) map[string]any {
	return map[string]any{
		"build": map[string]any{
			"result":          "REMOVED",
			"reportAvailable": false,
			"url":             buildURL,
		},
		"issues": []any{map[string]any{"kind": "build", "message": removedReportMessage}},
	}
}

func stageIssues(workflow map[string]any) (failed, notExecuted []map[string]any) {
	failed = []map[string]any{}
	notExecuted = []map[string]any{}
	for _, rawStage := range jsonutil.Array(workflow, "stages") {
		stage := jsonutil.Map(rawStage)
		status := jsonutil.String(stage["status"])
		if status == "SUCCESS" {
			continue
		}
		issue := map[string]any{"name": stage["name"], "status": stage["status"]}
		if errorValue := jsonutil.Map(stage["error"]); errorValue != nil {
			issue["message"] = jsonutil.FirstNonNil(errorValue["message"], errorValue["type"])
		}
		if status == "NOT_EXECUTED" {
			notExecuted = append(notExecuted, issue)
		} else {
			failed = append(failed, issue)
		}
	}
	return failed, notExecuted
}

func testReportIssues(report map[string]any) (map[string]any, []map[string]any) {
	if report == nil {
		return nil, []map[string]any{}
	}
	summary := map[string]any{}
	for _, key := range []string{"passCount", "failCount", "skipCount"} {
		if report[key] != nil {
			summary[key] = report[key]
		}
	}

	failures := []map[string]any{}
	for _, rawSuite := range jsonutil.Array(report, "suites") {
		suite := jsonutil.Map(rawSuite)
		for _, rawCase := range jsonutil.Array(suite, "cases") {
			testCase := jsonutil.Map(rawCase)
			switch jsonutil.String(testCase["status"]) {
			case "PASSED", "SKIPPED":
				continue
			}
			failure := map[string]any{
				"class":  jsonutil.FirstNonNil(testCase["className"], suite["name"]),
				"test":   testCase["name"],
				"status": testCase["status"],
			}
			message := textutil.Clean(jsonutil.FirstString(testCase["errorDetails"], testCase["errorStackTrace"]), maxIssueMessageLength)
			if message != "" {
				failure["message"] = message
			}
			failures = append(failures, failure)
		}
	}
	return summary, failures
}

func consoleHighlights(console string) []string {
	result := []string{}
	for _, line := range strings.Split(console, "\n") {
		if !slices.ContainsFunc(consoleHighlightPatterns, func(pattern *regexp.Regexp) bool {
			return pattern.MatchString(line)
		}) {
			continue
		}
		value := textutil.Clean(line, maxHighlightLength)
		if value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
		if len(result) == maxHighlights {
			break
		}
	}
	return result
}

func isoTimestamp(value any) any {
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	return time.UnixMilli(int64(number)).UTC().Format(time.RFC3339Nano)
}

func buildResult(build map[string]any) any {
	if result := build["result"]; result != nil && result != "" {
		return result
	}
	if building, _ := build["building"].(bool); building {
		return "RUNNING"
	}
	return nil
}
