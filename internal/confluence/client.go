// Package confluence reads compact Confluence page context.
package confluence

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jhoogstraat/deboai/internal/config"
	"github.com/jhoogstraat/deboai/internal/httpx"
	"github.com/jhoogstraat/deboai/internal/jsonutil"
	"github.com/jhoogstraat/deboai/internal/textutil"
)

const (
	DefaultAPIPath       = "rest/api"
	DefaultAttachmentDir = "confluence-analysis"
	maxTextLength        = 12000
	maxAttachments       = 100
	maxAttachmentSize    = 20 * 1024 * 1024
	attachmentDirectory  = "attachments"
)

var pageIDPattern = regexp.MustCompile(`^\d+$`)

// Options configures a Client.
type Options struct {
	// BaseURL is the Confluence root URL, usually ending in /wiki for Cloud.
	BaseURL string
	// APIPath is the REST API prefix below BaseURL.
	APIPath string
	// User enables basic authentication with Token, as required by Confluence Cloud.
	User string
	// Token is an API token when User is set, otherwise a bearer token.
	Token string
	// Cookie is an optional session cookie for SSO-protected instances.
	Cookie string
	// AttachmentDir is the repository-relative directory downloaded attachments use.
	AttachmentDir string
	// HTTPClient overrides the default non-redirecting HTTP client.
	HTTPClient *http.Client
}

// Client talks to the Confluence REST API.
type Client struct {
	baseURL       string
	apiPath       string
	user          string
	token         string
	cookie        string
	attachmentDir string
	http          *http.Client
}

// New builds a client from explicit options.
func New(options Options) (*Client, error) {
	if options.BaseURL == "" {
		return nil, fmt.Errorf("a Confluence URL is required")
	}
	client := options.HTTPClient
	if client == nil {
		client = httpx.NewNoRedirectClient()
	}
	return &Client{
		baseURL:       strings.TrimRight(options.BaseURL, "/"),
		apiPath:       valueOr(options.APIPath, DefaultAPIPath),
		user:          options.User,
		token:         options.Token,
		cookie:        options.Cookie,
		attachmentDir: valueOr(options.AttachmentDir, DefaultAttachmentDir),
		http:          client,
	}, nil
}

// FromValues builds a client from Confluence-specific configuration.
func FromValues(values config.Values) (*Client, error) {
	baseURL, err := values.Require("CONFLUENCE_URL")
	if err != nil {
		return nil, err
	}
	token, err := values.Require("CONFLUENCE_API_TOKEN")
	if err != nil {
		return nil, err
	}
	return New(Options{
		BaseURL:       baseURL,
		APIPath:       values.Value("CONFLUENCE_API_PATH"),
		User:          values.Value("CONFLUENCE_USER"),
		Token:         token,
		Cookie:        values.Value("CONFLUENCE_COOKIE"),
		AttachmentDir: values.Value("CONFLUENCE_ATTACHMENT_DIR"),
	})
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// Page fetches a page by ID or by a supported same-host Confluence page URL.
// URL forms with a pageId query or /pages/<id> or /content/<id> path use one
// API request. Legacy /display/<space>/<title> links use the title lookup endpoint.
func (c *Client) Page(ctx context.Context, rawPage string) (map[string]any, error) {
	return c.PageWithAttachment(ctx, rawPage, "", "")
}

// PageWithAttachment returns page context and downloads one attachment selected
// by ID or exact filename below root.
func (c *Client) PageWithAttachment(ctx context.Context, rawPage, root, attachmentSelector string) (map[string]any, error) {
	page, err := c.reference(rawPage)
	if err != nil {
		return nil, err
	}
	query := url.Values{
		"expand": {"body.storage,body.view,space,version"},
	}
	path := "content"
	if page.id != "" {
		path += "/" + url.PathEscape(page.id)
	} else {
		query.Set("title", page.title)
		query.Set("spaceKey", page.space)
		query.Set("limit", "2")
	}
	result, err := c.request(ctx, path, query)
	if err != nil {
		return nil, err
	}
	if page.id == "" {
		results := jsonutil.Array(result, "results")
		if len(results) == 0 {
			return nil, fmt.Errorf("confluence page not found: %s", rawPage)
		}
		if len(results) > 1 {
			return nil, fmt.Errorf("multiple Confluence pages match: %s", rawPage)
		}
		result = jsonutil.Map(results[0])
	}
	if result == nil {
		return nil, fmt.Errorf("unexpected Confluence page response")
	}
	compact := compactPage(result, strings.TrimSpace(rawPage))
	if strings.TrimSpace(attachmentSelector) == "" {
		return compact, nil
	}
	pageID := jsonutil.FirstText(result["id"], page.id)
	if pageID == "" {
		return nil, fmt.Errorf("confluence page response has no ID")
	}
	attachment, err := c.downloadSelectedAttachment(ctx, pageID, root, attachmentSelector)
	if err != nil {
		return nil, err
	}
	content := jsonutil.Map(compact["content"])
	if content == nil {
		content = map[string]any{}
		compact["content"] = content
	}
	content["attachment"] = attachment
	return compact, nil
}

type pageReference struct {
	id    string
	title string
	space string
}

func (c *Client) reference(rawPage string) (pageReference, error) {
	value := strings.TrimSpace(rawPage)
	if pageIDPattern.MatchString(value) {
		return pageReference{id: value}, nil
	}
	parsed, err := httpx.ParseURL(value)
	if err != nil {
		return pageReference{}, fmt.Errorf("invalid Confluence page URL: %s", rawPage)
	}
	base, err := httpx.ParseURL(c.baseURL)
	if err != nil {
		return pageReference{}, fmt.Errorf("invalid Confluence URL: %s", c.baseURL)
	}
	if !sameHost(parsed, base) {
		return pageReference{}, fmt.Errorf("unexpected host in Confluence page URL: %s", rawPage)
	}
	if id := parsed.Query().Get("pageId"); pageIDPattern.MatchString(id) {
		return pageReference{id: id}, nil
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	for index, segment := range segments {
		if (segment == "pages" || segment == "content") && index+1 < len(segments) && pageIDPattern.MatchString(segments[index+1]) {
			return pageReference{id: segments[index+1]}, nil
		}
	}
	for index, segment := range segments {
		if segment == "display" && index+2 < len(segments) {
			space, err := url.PathUnescape(segments[index+1])
			if err != nil {
				break
			}
			title, err := url.PathUnescape(strings.Join(segments[index+2:], "/"))
			if err != nil {
				break
			}
			return pageReference{space: space, title: strings.ReplaceAll(title, "+", " ")}, nil
		}
	}
	return pageReference{}, fmt.Errorf("confluence page URL has no supported page reference: %s", rawPage)
}

func sameHost(first, second *url.URL) bool {
	return strings.EqualFold(first.Scheme, second.Scheme) && strings.EqualFold(first.Host, second.Host)
}

func (c *Client) headers(accept string) map[string]string {
	headers := map[string]string{
		"Accept":            accept,
		"X-Atlassian-Token": "no-check",
	}
	if c.user != "" {
		headers["Authorization"] = httpx.BasicAuth(c.user, c.token)
	} else {
		headers["Authorization"] = "Bearer " + c.token
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
		if c.cookie == "" {
			return nil, fmt.Errorf("confluence authentication redirected to a login page (HTTP %d); the instance may need a session cookie in CONFLUENCE_COOKIE", response.Status)
		}
		return nil, fmt.Errorf("confluence authentication redirected to a login page (HTTP %d); refresh CONFLUENCE_COOKIE", response.Status)
	}
	if !response.OK() {
		return nil, httpx.APIError("Confluence", response.Status, response.Body)
	}
	var result map[string]any
	if err := httpx.DecodeJSON(response.Body, &result, "Confluence"); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) downloadSelectedAttachment(ctx context.Context, pageID, root, selector string) (map[string]any, error) {
	response, err := c.request(ctx, "content/"+url.PathEscape(pageID)+"/child/attachment", url.Values{"limit": {fmt.Sprint(maxAttachments)}})
	if err != nil {
		return nil, err
	}
	attachments := jsonutil.Array(response, "results")
	selected, err := attachmentIndex(attachments, selector)
	if err != nil {
		return nil, err
	}
	attachment := jsonutil.Map(attachments[selected])
	filename := filepath.Base(strings.ReplaceAll(jsonutil.FirstText(attachment["title"], attachment["id"], "attachment"), "\x00", ""))
	if filename == "." || filename == ".." || filename == "" {
		filename = "attachment-" + jsonutil.FirstText(attachment["id"])
	}
	links := jsonutil.Map(attachment["_links"])
	download := jsonutil.String(links["download"])
	if download == "" {
		return nil, fmt.Errorf("confluence attachment has no download URL: %s", filename)
	}
	rawURL := download
	if parsed, parseErr := httpx.ParseURL(download); parseErr != nil {
		rawURL = httpx.Join(c.baseURL, download)
	} else if base, baseErr := httpx.ParseURL(c.baseURL); baseErr != nil || !sameHost(parsed, base) {
		return nil, fmt.Errorf("unexpected host in Confluence attachment URL: %s", filename)
	}
	parsed, err := httpx.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Confluence attachment URL: %s", filename)
	}
	base, err := httpx.ParseURL(c.baseURL)
	if err != nil || !sameHost(parsed, base) {
		return nil, fmt.Errorf("unexpected host in Confluence attachment URL: %s", filename)
	}

	downloadResponse, err := httpx.Do(ctx, c.http, http.MethodGet, rawURL, c.headers("*/*"), maxAttachmentSize+1)
	if err != nil {
		return nil, err
	}
	if downloadResponse.Redirected() {
		return nil, fmt.Errorf("confluence attachment download redirected (HTTP %d)", downloadResponse.Status)
	}
	if !downloadResponse.OK() {
		return nil, httpx.APIError("Confluence attachment", downloadResponse.Status, downloadResponse.Body)
	}
	if len(downloadResponse.Body) > maxAttachmentSize {
		return nil, fmt.Errorf("the Confluence attachment is larger than %d bytes: %s", maxAttachmentSize, filename)
	}

	relative := filepath.Join(c.attachmentDir, pageID, attachmentDirectory, filename)
	outputPath, err := safePath(root, relative)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return nil, fmt.Errorf("create Confluence attachment directory: %w", err)
	}
	if err := os.WriteFile(outputPath, downloadResponse.Body, 0o644); err != nil {
		return nil, fmt.Errorf("save Confluence attachment %s: %w", filename, err)
	}
	metadata := jsonutil.Map(attachment["metadata"])
	extensions := jsonutil.Map(attachment["extensions"])
	return jsonutil.RemoveEmpty(map[string]any{
		"id":        attachment["id"],
		"filename":  filename,
		"mimeType":  metadata["mediaType"],
		"size":      extensions["fileSize"],
		"url":       rawURL,
		"localPath": filepath.ToSlash(relative),
	}), nil
}

func attachmentIndex(attachments []any, selector string) (int, error) {
	selector = strings.TrimSpace(selector)
	for index, rawAttachment := range attachments {
		if jsonutil.FirstText(jsonutil.Map(rawAttachment)["id"]) == selector {
			return index, nil
		}
	}
	selected := -1
	for index, rawAttachment := range attachments {
		if jsonutil.String(jsonutil.Map(rawAttachment)["title"]) != selector {
			continue
		}
		if selected >= 0 {
			return -1, fmt.Errorf("multiple Confluence attachments are named %s; select by ID", selector)
		}
		selected = index
	}
	if selected < 0 {
		return -1, fmt.Errorf("confluence attachment not found: %s", selector)
	}
	return selected, nil
}

func safePath(root, path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Join(root, path))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes repository root")
	}
	return absolute, nil
}

func compactPage(page map[string]any, fallbackURL string) map[string]any {
	links := jsonutil.Map(page["_links"])
	base := jsonutil.String(links["base"])
	webUI := jsonutil.String(links["webui"])
	pageURL := fallbackURL
	if base != "" && webUI != "" {
		pageURL = httpx.Join(base, webUI)
	}
	space := jsonutil.Map(page["space"])
	version := jsonutil.Map(page["version"])
	body := jsonutil.Map(page["body"])
	storage := jsonutil.Map(body["storage"])
	view := jsonutil.Map(body["view"])
	text := textutil.CleanHTML(jsonutil.FirstString(view["value"], storage["value"]), maxTextLength)
	return jsonutil.RemoveEmpty(map[string]any{
		"meta": jsonutil.RemoveEmpty(map[string]any{
			"id":      page["id"],
			"type":    page["type"],
			"status":  page["status"],
			"title":   page["title"],
			"url":     pageURL,
			"space":   jsonutil.RemoveEmpty(map[string]any{"key": space["key"], "name": space["name"]}),
			"version": jsonutil.RemoveEmpty(map[string]any{"number": version["number"], "when": version["when"]}),
		}),
		"content": jsonutil.RemoveEmpty(map[string]any{"body": text}),
	})
}
