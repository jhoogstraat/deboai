// Package jenkins reads build results, stage failures, and test failures from
// a Jenkins build.
package jenkins

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/httpx"
)

// DefaultBuildStatusName is the GitLab commit status a Jenkins pipeline
// publishes its build URL under.
const DefaultBuildStatusName = "build"

// Options configures a Client.
type Options struct {
	// BaseURL is the Jenkins root URL.
	BaseURL string
	// User and Token authenticate with the Jenkins API.
	User  string
	Token string
	// HTTPClient overrides the default HTTP client.
	HTTPClient *http.Client
}

// Client talks to the Jenkins remote access API.
type Client struct {
	baseURL string
	user    string
	token   string
	http    *http.Client
}

// New builds a client from explicit options.
func New(options Options) (*Client, error) {
	if options.BaseURL == "" {
		return nil, fmt.Errorf("a Jenkins URL is required")
	}
	client := options.HTTPClient
	if client == nil {
		client = httpx.NewClient()
	}
	return &Client{
		baseURL: strings.TrimRight(options.BaseURL, "/"),
		user:    options.User,
		token:   options.Token,
		http:    client,
	}, nil
}

// FromValues builds a client from JENKINS_URL, JENKINS_USER, and JENKINS_API_TOKEN.
func FromValues(values config.Values) (*Client, error) {
	baseURL, err := values.Require("JENKINS_URL")
	if err != nil {
		return nil, err
	}
	user, err := values.Require("JENKINS_USER")
	if err != nil {
		return nil, err
	}
	token, err := values.Require("JENKINS_API_TOKEN")
	if err != nil {
		return nil, err
	}
	return New(Options{BaseURL: baseURL, User: user, Token: token})
}

// BuildStatusName returns the configured GitLab commit status name that carries
// the Jenkins build URL.
func BuildStatusName(values config.Values) string {
	return values.ValueOr(DefaultBuildStatusName, "JENKINS_BUILD_STATUS_NAME")
}

// forBuild returns a copy of the client rooted at a single build URL.
func (c *Client) forBuild(buildURL string) *Client {
	copied := *c
	copied.baseURL = strings.TrimRight(buildURL, "/")
	return &copied
}

// request fetches a build sub-resource. When optional is set, a 404 yields a
// nil body, which is how Jenkins reports a discarded build report.
func (c *Client) request(ctx context.Context, path string, query url.Values, accept string, optional bool) ([]byte, error) {
	headers := map[string]string{"Accept": accept}
	if c.user != "" || c.token != "" {
		headers["Authorization"] = httpx.BasicAuth(c.user, c.token)
	}
	response, err := httpx.Do(ctx, c.http, http.MethodGet, httpx.WithQuery(httpx.Join(c.baseURL, path), query), headers, 0)
	if err != nil {
		return nil, err
	}
	if optional && response.Status == http.StatusNotFound {
		return nil, nil
	}
	if !response.OK() {
		return nil, httpx.APIError("Jenkins", response.Status, response.Body)
	}
	return response.Body, nil
}

func (c *Client) jsonRequest(ctx context.Context, path string, query url.Values, optional bool) (map[string]any, error) {
	body, err := c.request(ctx, path, query, "application/json", optional)
	if err != nil || body == nil {
		return nil, err
	}
	var result map[string]any
	if err := httpx.DecodeJSON(body, &result, "Jenkins"); err != nil {
		return nil, err
	}
	return result, nil
}

// NormalizeBuildURL trims console, API, and redirect suffixes from a build URL
// so it can be used as a request root.
func NormalizeBuildURL(value string) (string, error) {
	parsed, err := httpx.ParseURL(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("invalid Jenkins build URL: %s", value)
	}
	path := strings.TrimRight(parsed.Path, "/")
	for _, suffix := range []string{"/display/redirect", "/api/json", "/consoleText", "/wfapi/describe"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimRight(strings.TrimSuffix(path, suffix), "/")
			break
		}
	}
	if path == "" {
		return "", fmt.Errorf("invalid Jenkins build URL: %s", value)
	}
	parsed.Path = path + "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
