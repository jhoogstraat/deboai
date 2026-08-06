// Package jira reads compact issue context, including image attachments, from
// a Jira instance.
package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/jhoogstraat/ai-development-boost/internal/config"
	"github.com/jhoogstraat/ai-development-boost/internal/httpx"
)

// Defaults for the paths and directories that differ between Jira deployments.
const (
	DefaultAPIPath       = "rest/api/2"
	DefaultBrowsePath    = "browse"
	DefaultAttachmentDir = "ticket-analysis"
)

// TicketPattern matches a Jira issue key such as ABC-123.
var TicketPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)

// Options configures a Client.
type Options struct {
	// BaseURL is the Jira root URL.
	BaseURL string
	// APIPath is the REST API prefix below BaseURL.
	APIPath string
	// BrowsePath is the human-facing issue prefix below BaseURL.
	BrowsePath string
	// Token is a bearer token.
	Token string
	// Cookie is an optional session cookie, needed when Jira sits behind an
	// SSO proxy that a bearer token alone does not satisfy.
	Cookie string
	// AttachmentDir is the repository-relative directory image attachments
	// are downloaded into.
	AttachmentDir string
	// StatusNames maps Jira status IDs onto display names, for instances that
	// do not return a usable status name.
	StatusNames map[string]string
	// HTTPClient overrides the default non-redirecting HTTP client.
	HTTPClient *http.Client
}

// Client talks to the Jira REST API.
type Client struct {
	baseURL       string
	apiPath       string
	browsePath    string
	token         string
	cookie        string
	attachmentDir string
	statusNames   map[string]string
	http          *http.Client
}

// New builds a client from explicit options.
func New(options Options) (*Client, error) {
	if options.BaseURL == "" {
		return nil, fmt.Errorf("a Jira URL is required")
	}
	client := options.HTTPClient
	if client == nil {
		client = httpx.NewNoRedirectClient()
	}
	return &Client{
		baseURL:       strings.TrimRight(options.BaseURL, "/"),
		apiPath:       valueOr(options.APIPath, DefaultAPIPath),
		browsePath:    valueOr(options.BrowsePath, DefaultBrowsePath),
		token:         options.Token,
		cookie:        options.Cookie,
		attachmentDir: valueOr(options.AttachmentDir, DefaultAttachmentDir),
		statusNames:   options.StatusNames,
		http:          client,
	}, nil
}

// FromEnv builds a client from JIRA_URL and JIRA_API_TOKEN, plus the optional
// JIRA_API_PATH, JIRA_BROWSE_PATH, JIRA_COOKIE, JIRA_ATTACHMENT_DIR, and
// JIRA_STATUS_NAMES settings.
func FromEnv() (*Client, error) {
	baseURL, err := config.Require("JIRA_URL")
	if err != nil {
		return nil, err
	}
	token, err := config.Require("JIRA_API_TOKEN")
	if err != nil {
		return nil, err
	}
	return New(Options{
		BaseURL:       baseURL,
		APIPath:       config.Value("JIRA_API_PATH"),
		BrowsePath:    config.Value("JIRA_BROWSE_PATH"),
		Token:         token,
		Cookie:        config.Value("JIRA_COOKIE"),
		AttachmentDir: config.Value("JIRA_ATTACHMENT_DIR"),
		StatusNames:   config.Pairs("JIRA_STATUS_NAMES"),
	})
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// IssueURL returns the browser URL of an issue.
func (c *Client) IssueURL(key string) string {
	return httpx.Join(httpx.Join(c.baseURL, c.browsePath), key)
}

func (c *Client) headers(accept string) map[string]string {
	headers := map[string]string{
		"Accept":            accept,
		"Authorization":     "Bearer " + c.token,
		"X-Atlassian-Token": "no-check",
	}
	if c.cookie != "" {
		headers["Cookie"] = c.cookie
	}
	return headers
}

func (c *Client) request(ctx context.Context, path string, query url.Values) (map[string]any, error) {
	rawURL := httpx.WithQuery(httpx.Join(httpx.Join(c.baseURL, c.apiPath), path), query)
	response, err := httpx.Do(ctx, c.http, http.MethodGet, rawURL, c.headers("application/json"), 0)
	if err != nil {
		return nil, err
	}
	if response.Redirected() {
		return nil, c.loginRedirectError("Jira authentication", response.Status)
	}
	if !response.OK() {
		return nil, httpx.APIError("Jira", response.Status, response.Body)
	}
	var result map[string]any
	if err := httpx.DecodeJSON(response.Body, &result, "Jira"); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) loginRedirectError(subject string, status int) error {
	if c.cookie == "" {
		return fmt.Errorf("%s redirected to a login page (HTTP %d); the instance may need a session cookie in JIRA_COOKIE", subject, status)
	}
	return fmt.Errorf("%s redirected to a login page (HTTP %d); refresh JIRA_COOKIE", subject, status)
}
