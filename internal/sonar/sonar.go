// Package sonar reads failed quality gate conditions, uncovered new-code
// lines, and open issues from a SonarQube project.
package sonar

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/httpx"
	"github.com/jhoogstraat/deboai/internal/jsonutil"
	"github.com/jhoogstraat/deboai/internal/textutil"
)

// DefaultBranchPrefix is prepended to a local branch name, because SonarQube
// commonly analyses branches under their remote name.
const DefaultBranchPrefix = "origin/"

const pageSize = 500

// Options configures a Client.
type Options struct {
	// BaseURL is the SonarQube server URL.
	BaseURL string
	// Token authenticates as the basic-auth user name.
	Token string
	// ProjectKey identifies the analysed project.
	ProjectKey string
	// BranchPrefix is prepended to branch names that lack it. Set it to "-"
	// to disable prefixing.
	BranchPrefix string
	// HTTPClient overrides the default HTTP client.
	HTTPClient *http.Client
}

// Client talks to the SonarQube web API.
type Client struct {
	baseURL      string
	token        string
	projectKey   string
	branchPrefix string
	http         *http.Client
}

// New builds a client from explicit options.
func New(options Options) (*Client, error) {
	if options.BaseURL == "" {
		return nil, fmt.Errorf("SonarQube server URL is required")
	}
	if options.ProjectKey == "" {
		return nil, fmt.Errorf("SonarQube project key is required")
	}
	client := options.HTTPClient
	if client == nil {
		client = httpx.NewClient()
	}
	prefix := options.BranchPrefix
	if prefix == "" {
		prefix = DefaultBranchPrefix
	}
	if prefix == "-" {
		prefix = ""
	}
	return &Client{
		baseURL:      strings.TrimRight(options.BaseURL, "/"),
		token:        options.Token,
		projectKey:   options.ProjectKey,
		branchPrefix: prefix,
		http:         client,
	}, nil
}

// BaseURLFromValues returns the configured SonarQube server URL.
func BaseURLFromValues(values config.Values) (string, error) {
	return values.Require("SONAR_HOST_URL", "SONARQUBE_CLI_SERVER")
}

// FromValues builds a client from SONAR_HOST_URL (or SONARQUBE_CLI_SERVER),
// SONAR_TOKEN (or SONARQUBE_CLI_TOKEN), SONAR_PROJECT_KEY, and the optional
// SONAR_BRANCH_PREFIX.
func FromValues(values config.Values) (*Client, error) {
	projectKey, err := values.Require("SONAR_PROJECT_KEY")
	if err != nil {
		return nil, err
	}
	return FromValuesWithProjectKey(values, projectKey)
}

// FromValuesWithProjectKey builds a client using an explicit project key and the
// remaining SonarQube settings from request-scoped configuration. It supports
// callers that securely derived the key from a commit's GitLab status.
func FromValuesWithProjectKey(values config.Values, projectKey string) (*Client, error) {
	baseURL, err := BaseURLFromValues(values)
	if err != nil {
		return nil, err
	}
	token, err := values.Require("SONAR_TOKEN", "SONARQUBE_CLI_TOKEN")
	if err != nil {
		return nil, err
	}
	return New(Options{
		BaseURL:      baseURL,
		Token:        token,
		ProjectKey:   projectKey,
		BranchPrefix: values.Value("SONAR_BRANCH_PREFIX"),
	})
}

// ProjectKeyFromURL returns a project key only when targetURL points to the
// configured SonarQube server. This deliberately does not trust an arbitrary
// URL supplied by an external CI status.
func ProjectKeyFromURL(baseURL, targetURL string) (string, bool) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", false
	}
	target, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", false
	}
	if !strings.EqualFold(base.Scheme, target.Scheme) || !strings.EqualFold(base.Host, target.Host) {
		return "", false
	}
	basePath := strings.TrimRight(base.EscapedPath(), "/")
	if basePath != "" && target.EscapedPath() != basePath && !strings.HasPrefix(target.EscapedPath(), basePath+"/") {
		return "", false
	}
	key := strings.TrimSpace(target.Query().Get("id"))
	return key, key != ""
}

// BranchName returns the SonarQube branch name for a local branch.
func (c *Client) BranchName(branch string) string {
	if c.branchPrefix == "" || strings.HasPrefix(branch, c.branchPrefix) {
		return branch
	}
	return c.branchPrefix + branch
}

func (c *Client) api(ctx context.Context, path string, query url.Values) (map[string]any, error) {
	response, err := httpx.Get(ctx, c.http, httpx.WithQuery(httpx.Join(c.baseURL, path), query), map[string]string{
		"Accept":        "application/json",
		"Authorization": httpx.BasicAuth(c.token, ""),
	})
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		return nil, httpx.APIError("SonarQube", response.Status, response.Body)
	}
	var result map[string]any
	if err := httpx.DecodeJSON(response.Body, &result, "SonarQube"); err != nil {
		return nil, err
	}
	return result, nil
}

// Issues returns the failed quality gate conditions of a branch, the new-code
// lines that are missing coverage, and the confirmed or open issues.
func (c *Client) Issues(ctx context.Context, branch string) (map[string]any, error) {
	sonarBranch := c.BranchName(strings.TrimSpace(branch))
	if sonarBranch == "" {
		return nil, fmt.Errorf("a SonarQube branch name is required")
	}
	if err := c.requireBranch(ctx, sonarBranch); err != nil {
		return nil, err
	}

	qualityGate, err := c.api(ctx, "/api/qualitygates/project_status", url.Values{
		"projectKey": {c.projectKey},
		"branch":     {sonarBranch},
	})
	if err != nil {
		return nil, err
	}
	failedConditions := failedConditions(jsonutil.Map(qualityGate["projectStatus"]))

	coverageFiles := []any{}
	if hasFailedCoverageCondition(failedConditions) {
		if coverageFiles, err = c.coverageFiles(ctx, sonarBranch); err != nil {
			return nil, err
		}
	}
	issues, err := c.issues(ctx, sonarBranch)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"failedConditions": failedConditions,
		"coverageFiles":    coverageFiles,
		"issues":           issues,
	}, nil
}

func (c *Client) requireBranch(ctx context.Context, sonarBranch string) error {
	branches, err := c.api(ctx, "/api/project_branches/list", url.Values{"project": {c.projectKey}})
	if err != nil {
		return err
	}
	for _, rawBranch := range jsonutil.Array(branches, "branches") {
		if jsonutil.String(jsonutil.Map(rawBranch)["name"]) == sonarBranch {
			return nil
		}
	}
	return fmt.Errorf("SonarQube branch not found: %s", sonarBranch)
}

func (c *Client) issues(ctx context.Context, sonarBranch string) ([]any, error) {
	issues := []any{}
	for page := 1; ; page++ {
		response, err := c.api(ctx, "/api/issues/search", url.Values{
			"componentKeys":   {c.projectKey},
			"branch":          {sonarBranch},
			"inNewCodePeriod": {"true"},
			"statuses":        {"CONFIRMED,OPEN"},
			"ps":              {fmt.Sprint(pageSize)},
			"p":               {fmt.Sprint(page)},
		})
		if err != nil {
			return nil, err
		}
		pageIssues := jsonutil.Array(response, "issues")
		for _, rawIssue := range pageIssues {
			issues = append(issues, CompactIssue(jsonutil.Map(rawIssue)))
		}
		total, _ := jsonutil.Map(response["paging"])["total"].(float64)
		if len(issues) >= int(total) || len(pageIssues) == 0 {
			return issues, nil
		}
	}
}

func failedConditions(projectStatus map[string]any) []any {
	failed := []any{}
	for _, rawCondition := range jsonutil.Array(projectStatus, "conditions") {
		condition := jsonutil.Map(rawCondition)
		if jsonutil.String(condition["status"]) == "ERROR" {
			failed = append(failed, condition)
		}
	}
	return failed
}

func hasFailedCoverageCondition(conditions []any) bool {
	for _, rawCondition := range conditions {
		if strings.HasSuffix(jsonutil.String(jsonutil.Map(rawCondition)["metricKey"]), "coverage") {
			return true
		}
	}
	return false
}

// coverageFiles lists the new-code lines that are uncovered or only partially
// covered, per file.
func (c *Client) coverageFiles(ctx context.Context, sonarBranch string) ([]any, error) {
	components, err := c.uncoveredComponents(ctx, sonarBranch)
	if err != nil {
		return nil, err
	}

	coverageFiles := []any{}
	for _, rawComponent := range components {
		component := jsonutil.Map(rawComponent)
		if !hasUncoveredCoverage(component) {
			continue
		}
		response, err := c.api(ctx, "/api/sources/lines", url.Values{
			"key":    {jsonutil.String(component["key"])},
			"branch": {sonarBranch},
		})
		if err != nil {
			return nil, err
		}
		uncovered, partial := coverageLines(jsonutil.Array(response, "sources"))
		if len(uncovered) > 0 || len(partial) > 0 {
			coverageFiles = append(coverageFiles, map[string]any{
				"path":                  component["path"],
				"uncoveredLines":        uncovered,
				"partiallyCoveredLines": partial,
			})
		}
	}
	return coverageFiles, nil
}

func (c *Client) uncoveredComponents(ctx context.Context, sonarBranch string) ([]any, error) {
	components := []any{}
	for page := 1; ; page++ {
		response, err := c.api(ctx, "/api/measures/component_tree", url.Values{
			"component":  {c.projectKey},
			"branch":     {sonarBranch},
			"metricKeys": {"new_uncovered_lines,new_uncovered_conditions"},
			"qualifiers": {"FIL"},
			"ps":         {fmt.Sprint(pageSize)},
			"p":          {fmt.Sprint(page)},
		})
		if err != nil {
			return nil, err
		}
		pageComponents := jsonutil.Array(response, "components")
		components = append(components, pageComponents...)
		total, _ := jsonutil.Map(response["paging"])["total"].(float64)
		if len(components) >= int(total) || len(pageComponents) == 0 {
			return components, nil
		}
	}
}

func coverageLines(sources []any) (uncovered, partial []any) {
	uncovered = []any{}
	partial = []any{}
	for _, rawSource := range sources {
		source := jsonutil.Map(rawSource)
		if isNew, _ := source["isNew"].(bool); !isNew {
			continue
		}
		lineHits, hasLineHits := source["lineHits"].(float64)
		conditions, hasConditions := source["conditions"].(float64)
		coveredConditions, _ := source["coveredConditions"].(float64)

		line := map[string]any{"line": source["line"]}
		if hasConditions {
			line["conditions"] = conditions
			line["coveredConditions"] = coveredConditions
		}
		if code := strings.TrimSpace(textutil.StripMarkup(jsonutil.String(source["code"]))); code != "" {
			line["code"] = code
		}
		switch {
		case (hasLineHits && lineHits == 0) || (hasConditions && coveredConditions == 0):
			uncovered = append(uncovered, line)
		case hasConditions && coveredConditions < conditions:
			partial = append(partial, line)
		}
	}
	return uncovered, partial
}

func hasUncoveredCoverage(component map[string]any) bool {
	for _, rawMeasure := range jsonutil.Array(component, "measures") {
		measure := jsonutil.Map(rawMeasure)
		metric := jsonutil.String(measure["metric"])
		if metric != "new_uncovered_lines" && metric != "new_uncovered_conditions" {
			continue
		}
		if jsonutil.String(jsonutil.Map(measure["period"])["value"]) != "0" {
			return true
		}
	}
	return false
}

// CompactIssue reduces an issue to its rule, location, and message.
func CompactIssue(issue map[string]any) map[string]any {
	result := map[string]any{}
	for _, field := range []string{"severity", "rule", "component", "message", "scope"} {
		if issue[field] != nil {
			result[field] = issue[field]
		}
	}
	if textRange := jsonutil.Map(issue["textRange"]); textRange != nil {
		result["lineRange"] = []any{textRange["startLine"], textRange["endLine"]}
	} else if issue["line"] != nil {
		result["lineRange"] = []any{issue["line"], issue["line"]}
	}
	return result
}
