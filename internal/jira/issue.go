package jira

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jhoogstraat/ai-development-boost/internal/httpx"
	"github.com/jhoogstraat/ai-development-boost/internal/jsonutil"
	"github.com/jhoogstraat/ai-development-boost/internal/textutil"
)

const (
	maxTextLength       = 8000
	maxCommentLength    = 4000
	maxComments         = 100
	maxLinks            = 100
	maxAttachments      = 100
	maxValueItems       = 20
	maxScreenshotSize   = 20 * 1024 * 1024
	issueFields         = "assignee,attachment,comment,components,created,creator,description,duedate,environment,fixVersions,issuelinks,issuetype,labels,priority,project,reporter,resolution,resolutiondate,status,summary,updated,versions"
	attachmentDirectory = "attachments"
)

// Issue returns the compact context of a single issue. Image attachments are
// downloaded below root, and their repository-relative paths are reported.
func (c *Client) Issue(ctx context.Context, rawTicket, root string) (map[string]any, error) {
	ticket := strings.ToUpper(strings.TrimSpace(rawTicket))
	if !TicketPattern.MatchString(ticket) {
		return nil, fmt.Errorf("invalid Jira ticket key: %s", rawTicket)
	}
	issue, err := c.request(ctx, "issue/"+ticket, url.Values{"fields": {issueFields}})
	if err != nil {
		return nil, err
	}
	fields := jsonutil.Map(issue["fields"])
	if fields == nil {
		return nil, fmt.Errorf("unexpected Jira issue response for %s", ticket)
	}

	meta := map[string]any{
		"key":         issue["key"],
		"id":          issue["id"],
		"url":         c.IssueURL(jsonutil.String(jsonutil.FirstNonNil(issue["key"], ticket))),
		"summary":     fields["summary"],
		"issueType":   compactValue(fields["issuetype"], maxTextLength),
		"status":      c.compactStatus(fields["status"]),
		"priority":    compactValue(fields["priority"], maxTextLength),
		"project":     compactValue(fields["project"], maxTextLength),
		"assignee":    compactIdentity(fields["assignee"]),
		"reporter":    compactIdentity(fields["reporter"]),
		"creator":     compactIdentity(fields["creator"]),
		"created":     fields["created"],
		"updated":     fields["updated"],
		"dueDate":     fields["duedate"],
		"labels":      fields["labels"],
		"components":  compactValue(fields["components"], maxTextLength),
		"fixVersions": compactValue(fields["fixVersions"], maxTextLength),
		"versions":    compactValue(fields["versions"], maxTextLength),
	}
	attachments, err := c.compactAttachments(ctx, fields, ticket, root)
	if err != nil {
		return nil, err
	}
	content := map[string]any{
		"description": compactText(fields["description"], maxTextLength),
		"environment": compactText(fields["environment"], maxTextLength),
		"comments":    c.compactComments(fields),
		"links":       c.compactLinks(fields),
		"attachments": attachments,
	}
	return map[string]any{
		"meta":    jsonutil.RemoveEmpty(meta),
		"content": jsonutil.RemoveEmpty(content),
	}, nil
}

func compactText(value any, limit int) string {
	if value == nil {
		return ""
	}
	var text string
	switch parsed := value.(type) {
	case string:
		text = parsed
	case []any:
		parts := make([]string, 0, len(parsed))
		for _, item := range parsed {
			if part := compactText(item, limit); part != "" {
				parts = append(parts, part)
			}
		}
		text = strings.Join(parts, "\n")
	case map[string]any:
		switch {
		case parsed["text"] != nil:
			text = fmt.Sprint(parsed["text"])
		case parsed["body"] != nil:
			text = compactText(parsed["body"], limit)
		case parsed["content"] != nil:
			text = compactText(parsed["content"], limit)
		default:
			for _, key := range []string{"value", "name", "displayName", "key"} {
				if !jsonutil.IsEmpty(parsed[key]) {
					text = fmt.Sprint(parsed[key])
					break
				}
			}
		}
	default:
		text = fmt.Sprint(value)
	}
	return textutil.Clean(text, limit)
}

func compactValue(value any, limit int) any {
	switch parsed := value.(type) {
	case []any:
		items := parsed
		if len(items) > maxValueItems {
			items = items[:maxValueItems]
		}
		result := make([]any, 0, len(items))
		for _, item := range items {
			result = append(result, compactValue(item, limit))
		}
		return result
	case map[string]any:
		selected := map[string]any{}
		for _, key := range []string{"id", "key", "name", "value", "displayName", "username", "type"} {
			if !jsonutil.IsEmpty(parsed[key]) {
				selected[key] = compactValue(parsed[key], limit)
			}
		}
		if len(selected) > 0 {
			return selected
		}
		return compactText(parsed, limit)
	case string:
		return compactText(parsed, limit)
	default:
		return value
	}
}

func compactIdentity(value any) any {
	parsed := jsonutil.Map(value)
	if parsed == nil {
		return compactValue(value, maxTextLength)
	}
	result := map[string]any{}
	for _, key := range []string{"accountId", "key", "name", "displayName", "active"} {
		if !jsonutil.IsEmpty(parsed[key]) {
			result[key] = parsed[key]
		}
	}
	return result
}

func (c *Client) compactStatus(value any) any {
	parsed := jsonutil.Map(value)
	if parsed == nil {
		return compactValue(value, maxTextLength)
	}
	statusID := fmt.Sprint(parsed["id"])
	if name := c.statusNames[statusID]; name != "" {
		return map[string]any{"id": statusID, "value": name}
	}
	return compactValue(parsed, maxTextLength)
}

func (c *Client) compactComments(fields map[string]any) []any {
	comments := jsonutil.Array(jsonutil.Map(fields["comment"]), "comments")
	if len(comments) > maxComments {
		comments = comments[:maxComments]
	}
	result := make([]any, 0, len(comments))
	for _, rawComment := range comments {
		comment := jsonutil.Map(rawComment)
		result = append(result, jsonutil.RemoveEmpty(map[string]any{
			"author":  compactIdentity(comment["author"]),
			"created": comment["created"],
			"updated": comment["updated"],
			"body":    compactText(comment["body"], maxCommentLength),
		}))
	}
	return result
}

func (c *Client) compactLinks(fields map[string]any) []any {
	links := jsonutil.Array(fields, "issuelinks")
	if len(links) > maxLinks {
		links = links[:maxLinks]
	}
	result := make([]any, 0, len(links))
	for _, rawLink := range links {
		link := jsonutil.Map(rawLink)
		linkType := jsonutil.Map(link["type"])
		direction := "outward"
		issue := link["outwardIssue"]
		if link["inwardIssue"] != nil {
			direction = "inward"
			issue = link["inwardIssue"]
		}
		result = append(result, jsonutil.RemoveEmpty(map[string]any{
			"type":      jsonutil.FirstNonNil(linkType[direction], linkType["name"]),
			"direction": direction,
			"issue":     c.compactIssueReference(issue),
		}))
	}
	return result
}

func (c *Client) compactIssueReference(value any) map[string]any {
	issue := jsonutil.Map(value)
	fields := jsonutil.Map(issue["fields"])
	result := map[string]any{"key": issue["key"]}
	if fields["summary"] != nil {
		result["summary"] = fields["summary"]
	}
	if fields["status"] != nil {
		result["status"] = c.compactStatus(fields["status"])
	}
	return jsonutil.RemoveEmpty(result)
}

func (c *Client) compactAttachments(ctx context.Context, fields map[string]any, ticket, root string) ([]any, error) {
	attachments := jsonutil.Array(fields, "attachment")
	if len(attachments) > maxAttachments {
		attachments = attachments[:maxAttachments]
	}
	result := make([]any, 0, len(attachments))
	for _, rawAttachment := range attachments {
		attachment := jsonutil.Map(rawAttachment)
		item := map[string]any{
			"filename": attachment["filename"],
			"mimeType": attachment["mimeType"],
			"size":     attachment["size"],
			"created":  attachment["created"],
			"url":      attachment["content"],
		}
		if strings.HasPrefix(jsonutil.String(attachment["mimeType"]), "image/") {
			path, err := c.downloadImage(ctx, attachment, ticket, root)
			if err != nil {
				return nil, err
			}
			item["localPath"] = path
		}
		result = append(result, jsonutil.RemoveEmpty(item))
	}
	return result, nil
}

// downloadImage stores an image attachment below root and returns its
// repository-relative path.
func (c *Client) downloadImage(ctx context.Context, attachment map[string]any, ticket, root string) (string, error) {
	filename := filepath.Base(strings.ReplaceAll(jsonutil.FirstText(attachment["filename"], attachment["id"], "screenshot"), "\x00", ""))
	if filename == "." || filename == ".." || filename == "" {
		filename = "attachment-" + fmt.Sprint(attachment["id"])
	}
	contentURL := jsonutil.String(attachment["content"])
	parsed, err := httpx.ParseURL(contentURL)
	if err != nil {
		return "", fmt.Errorf("invalid Jira attachment URL: %s", filename)
	}
	base, err := httpx.ParseURL(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid Jira URL: %s", c.baseURL)
	}
	if parsed.Scheme != base.Scheme || parsed.Host != base.Host {
		return "", fmt.Errorf("unexpected host in Jira attachment URL: %s", filename)
	}

	response, err := httpx.Do(ctx, c.http, http.MethodGet, contentURL, c.headers("image/*"), maxScreenshotSize+1)
	if err != nil {
		return "", err
	}
	if response.Redirected() {
		return "", c.loginRedirectError("Jira attachment download", response.Status)
	}
	if !response.OK() {
		return "", httpx.APIError("Jira attachment", response.Status, response.Body)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if !strings.HasPrefix(contentType, "image/") {
		return "", fmt.Errorf("the Jira attachment is not an image: %s", filename)
	}
	if len(response.Body) > maxScreenshotSize {
		return "", fmt.Errorf("the Jira attachment is larger than %d bytes: %s", maxScreenshotSize, filename)
	}

	relative := filepath.Join(c.attachmentDir, ticket, attachmentDirectory, filename)
	outputPath, err := safePath(root, relative)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", fmt.Errorf("create Jira attachment directory: %w", err)
	}
	if err := os.WriteFile(outputPath, response.Body, 0o644); err != nil {
		return "", fmt.Errorf("save Jira attachment %s: %w", filename, err)
	}
	return filepath.ToSlash(relative), nil
}

// safePath resolves path below root and rejects anything that escapes it.
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
