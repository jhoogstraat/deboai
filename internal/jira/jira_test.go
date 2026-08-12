package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestTicketPattern(t *testing.T) {
	for _, ticket := range []string{"ABC-1", "AB1-4711"} {
		if !TicketPattern.MatchString(ticket) {
			t.Fatalf("TicketPattern rejected %q", ticket)
		}
	}
	for _, ticket := range []string{"abc-1", "A-1", "ABC1", "ABC-", "ABC-1x"} {
		if TicketPattern.MatchString(ticket) {
			t.Fatalf("TicketPattern accepted %q", ticket)
		}
	}
}

func TestSafePathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	if _, err := safePath(root, filepath.Join("..", "outside")); err == nil {
		t.Fatal("safePath accepted a path outside the repository")
	}
	if _, err := safePath(root, filepath.Join("inside", "file.png")); err != nil {
		t.Fatal(err)
	}
}

func TestCompactValueSelectsIdentifyingFields(t *testing.T) {
	actual := compactValue(map[string]any{
		"id": "10", "name": "Bug", "self": "https://jira.example/rest/api/2/issuetype/10",
	}, maxTextLength)
	selected, _ := actual.(map[string]any)
	if selected["id"] != "10" || selected["name"] != "Bug" {
		t.Fatalf("compactValue() = %#v", selected)
	}
	if _, ok := selected["self"]; ok {
		t.Fatalf("compactValue() kept an unexpected field: %#v", selected)
	}
}

func TestCompactStatusUsesConfiguredNames(t *testing.T) {
	client, err := New(Options{BaseURL: "https://jira.example", StatusNames: map[string]string{"18820": "In Review"}})
	if err != nil {
		t.Fatal(err)
	}
	actual := client.compactStatus(map[string]any{"id": "18820", "name": "Custom"})
	expected := map[string]any{"id": "18820", "value": "In Review"}
	if status, _ := actual.(map[string]any); status["value"] != expected["value"] {
		t.Fatalf("compactStatus() = %#v, want %#v", actual, expected)
	}
}

func TestIssueDownloadsImageAttachments(t *testing.T) {
	root := t.TempDir()
	var jira *httptest.Server
	jira = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue/ABC-1":
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"key": "ABC-1", "id": "1",
				"fields": map[string]any{
					"summary":     "Broken layout",
					"description": "<p>Fix the <b>layout</b></p>",
					"status":      map[string]any{"id": "1", "name": "Open"},
					"attachment": []any{
						map[string]any{"filename": "shot.png", "mimeType": "image/png", "content": jira.URL + "/attachment/1"},
						map[string]any{"filename": "notes.txt", "mimeType": "text/plain", "content": jira.URL + "/attachment/2"},
					},
				},
			})
		case "/attachment/1":
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write([]byte("png-bytes"))
		case "/attachment/2":
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = writer.Write([]byte("notes"))
		default:
			http.Error(writer, "unexpected endpoint", http.StatusNotFound)
		}
	}))
	defer jira.Close()

	client, err := New(Options{BaseURL: jira.URL, Token: "token", HTTPClient: jira.Client()})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := client.Issue(context.Background(), "abc-1", root)
	if err != nil {
		t.Fatal(err)
	}

	meta, _ := issue["meta"].(map[string]any)
	if meta["summary"] != "Broken layout" {
		t.Fatalf("Issue() meta = %#v", meta)
	}
	content, _ := issue["content"].(map[string]any)
	if content["description"] != "Fix the layout" {
		t.Fatalf("Issue() description = %#v, want the markup stripped", content["description"])
	}

	attachments, _ := content["attachments"].([]any)
	if len(attachments) != 2 {
		t.Fatalf("Issue() attachments = %#v", attachments)
	}
	image, _ := attachments[0].(map[string]any)
	expectedPath := filepath.Join(DefaultAttachmentDir, "ABC-1", attachmentDirectory, "shot.png")
	if image["localPath"] != filepath.ToSlash(expectedPath) {
		t.Fatalf("Issue() localPath = %#v, want %q", image["localPath"], expectedPath)
	}
	if _, err := os.Stat(filepath.Join(root, expectedPath)); err != nil {
		t.Fatalf("attachment was not written: %v", err)
	}
	if text, _ := attachments[1].(map[string]any); text["localPath"] != nil {
		t.Fatalf("Issue() downloaded a non-image attachment: %#v", text)
	}

	issue, err = client.IssueWithAttachment(context.Background(), "abc-1", root, "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	content, _ = issue["content"].(map[string]any)
	attachments, _ = content["attachments"].([]any)
	notes, _ := attachments[1].(map[string]any)
	expectedPath = filepath.Join(DefaultAttachmentDir, "ABC-1", attachmentDirectory, "notes.txt")
	if notes["localPath"] != filepath.ToSlash(expectedPath) {
		t.Fatalf("selected attachment localPath = %#v, want %q", notes["localPath"], expectedPath)
	}
	if data, err := os.ReadFile(filepath.Join(root, expectedPath)); err != nil || string(data) != "notes" {
		t.Fatalf("selected attachment = %q, %v", data, err)
	}
}

func TestIssueRejectsInvalidTickets(t *testing.T) {
	client, err := New(Options{BaseURL: "https://jira.example", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Issue(context.Background(), "not-a-ticket", t.TempDir()); err == nil {
		t.Fatal("Issue() accepted an invalid ticket key")
	}
}

func TestIssueReportsLoginRedirects(t *testing.T) {
	jira := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Redirect(writer, &http.Request{}, "https://login.example", http.StatusFound)
	}))
	defer jira.Close()

	client, err := New(Options{BaseURL: jira.URL, Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Issue(context.Background(), "ABC-1", t.TempDir()); err == nil {
		t.Fatal("Issue() followed a login redirect")
	}
}
