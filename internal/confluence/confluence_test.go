package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageFetchesAndCompactsPageByID(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rest/api/content/123" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.URL.Query().Get("expand") != "body.storage,body.view,space,version" {
			t.Fatalf("expand = %q", request.URL.Query().Get("expand"))
		}
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"id": "123", "type": "page", "status": "current", "title": "Runbook",
			"space":   map[string]any{"key": "OPS", "name": "Operations", "_links": map[string]any{"self": "ignored"}},
			"version": map[string]any{"number": 4, "when": "2026-08-12T10:00:00Z"},
			"body": map[string]any{
				"storage": map[string]any{"value": "<p>Raw macro source</p>"},
				"view":    map[string]any{"value": "<p>Deploy &amp; verify</p><p>Check logs</p>"},
			},
			"_links": map[string]any{"base": server.URL, "webui": "/wiki/spaces/OPS/pages/123/Runbook"},
		})
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Page(context.Background(), "123")
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := page["meta"].(map[string]any)
	if meta["title"] != "Runbook" || meta["url"] != server.URL+"/wiki/spaces/OPS/pages/123/Runbook" {
		t.Fatalf("meta = %#v", meta)
	}
	content, _ := page["content"].(map[string]any)
	if content["body"] != "Deploy & verify\nCheck logs" {
		t.Fatalf("content = %#v", content)
	}
}

func TestPageLooksUpLegacyDisplayURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rest/api/content" || request.URL.Query().Get("spaceKey") != "OPS" || request.URL.Query().Get("title") != "Deploy Guide" {
			t.Fatalf("lookup request = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"results": []any{map[string]any{
			"id": "9", "title": "Deploy Guide", "body": map[string]any{"view": map[string]any{"value": "<h1>Steps</h1>"}},
		}}})
	}))
	defer server.Close()
	client, err := New(Options{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Page(context.Background(), server.URL+"/display/OPS/Deploy+Guide")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := page["content"].(map[string]any)
	if content["body"] != "Steps" {
		t.Fatalf("content = %#v", content)
	}
}

func TestFromValuesUsesOnlyConfluenceConfiguration(t *testing.T) {
	client, err := FromValues(map[string]string{
		"CONFLUENCE_URL":       "https://confluence.example.test/wiki",
		"CONFLUENCE_USER":      "user@example.test",
		"CONFLUENCE_API_TOKEN": "confluence-token",
		"CONFLUENCE_COOKIE":    "confluence=1",
		"JIRA_URL":             "https://jira.example.test",
		"JIRA_API_TOKEN":       "jira-token",
		"JIRA_COOKIE":          "jira=1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.baseURL != "https://confluence.example.test/wiki" || client.apiPath != DefaultAPIPath || client.user != "user@example.test" || client.token != "confluence-token" || client.cookie != "confluence=1" {
		t.Fatalf("client = %#v", client)
	}
}

func TestFromValuesRequiresConfluenceConfiguration(t *testing.T) {
	for _, values := range []map[string]string{
		{"JIRA_URL": "https://jira.example.test", "JIRA_API_TOKEN": "jira-token"},
		{"CONFLUENCE_URL": "https://confluence.example.test/wiki", "JIRA_API_TOKEN": "jira-token"},
	} {
		if _, err := FromValues(values); err == nil {
			t.Fatalf("FromValues(%#v) succeeded", values)
		}
	}
}

func TestPageUsesBasicAuthWhenUserIsConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		user, token, ok := request.BasicAuth()
		if !ok || user != "user@example.test" || token != "token" {
			t.Fatalf("basic auth = %q, %q, %v", user, token, ok)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"id": "123", "title": "Runbook"})
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL, User: "user@example.test", Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Page(context.Background(), "123"); err != nil {
		t.Fatal(err)
	}
}

func TestPageDownloadsSelectedAttachment(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/content/123":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "123", "title": "Runbook"})
		case "/rest/api/content/123/child/attachment":
			_ = json.NewEncoder(writer).Encode(map[string]any{"results": []any{
				map[string]any{"id": "att1", "title": "diagram.pdf", "metadata": map[string]any{"mediaType": "application/pdf"}, "extensions": map[string]any{"fileSize": 7}, "_links": map[string]any{"download": "/download/attachments/123/diagram.pdf"}},
			}})
		case "/download/attachments/123/diagram.pdf":
			writer.Header().Set("Content-Type", "application/pdf")
			_, _ = writer.Write([]byte("pdfdata"))
		default:
			http.Error(writer, "unexpected endpoint", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.PageWithAttachment(context.Background(), "123", root, "diagram.pdf")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := page["content"].(map[string]any)
	attachment, _ := content["attachment"].(map[string]any)
	expectedPath := filepath.Join(DefaultAttachmentDir, "123", attachmentDirectory, "diagram.pdf")
	if attachment["localPath"] != filepath.ToSlash(expectedPath) {
		t.Fatalf("attachment = %#v", attachment)
	}
	if data, err := os.ReadFile(filepath.Join(root, expectedPath)); err != nil || string(data) != "pdfdata" {
		t.Fatalf("downloaded attachment = %q, %v", data, err)
	}
}

func TestPageLooksUpLegacyDisplayURLWithEscapedSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("title") != "Deploy/Guide" {
			t.Fatalf("title = %q", request.URL.Query().Get("title"))
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"results": []any{map[string]any{"id": "9", "title": "Deploy/Guide"}}})
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Page(context.Background(), server.URL+"/display/OPS/Deploy%2FGuide"); err != nil {
		t.Fatal(err)
	}
}

func TestPageRejectsOtherHosts(t *testing.T) {
	client, err := New(Options{BaseURL: "https://example.test", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := url.Parse("https://other.test/wiki/pages/1/Nope")
	if _, err := client.Page(context.Background(), other.String()); err == nil || !strings.Contains(err.Error(), "unexpected host") {
		t.Fatalf("Page() error = %v", err)
	}
}
