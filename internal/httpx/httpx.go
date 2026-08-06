// Package httpx provides the small HTTP and JSON conveniences shared by the
// development tool clients.
package httpx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds every outgoing request.
const DefaultTimeout = 60 * time.Second

// Response is the part of an HTTP response the clients care about.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// OK reports whether the response carries a 2xx status.
func (r Response) OK() bool {
	return r.Status >= 200 && r.Status < 300
}

// Redirected reports whether the response carries a 3xx status, which the
// token-authenticated APIs use to bounce unauthenticated callers to a login page.
func (r Response) Redirected() bool {
	return r.Status >= 300 && r.Status < 400
}

// NewClient returns the default client used when a caller supplies none.
func NewClient() *http.Client {
	return &http.Client{Timeout: DefaultTimeout}
}

// NewNoRedirectClient returns a client that surfaces redirects to the caller
// instead of following them.
func NewNoRedirectClient() *http.Client {
	return &http.Client{
		Timeout: DefaultTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Get performs a GET request and reads the whole response body.
func Get(ctx context.Context, client *http.Client, rawURL string, headers map[string]string) (Response, error) {
	return Do(ctx, client, http.MethodGet, rawURL, headers, 0)
}

// Do performs a request and reads at most maxBytes of the response body. A
// maxBytes of zero reads the body in full.
func Do(ctx context.Context, client *http.Client, method, rawURL string, headers map[string]string, maxBytes int64) (Response, error) {
	if client == nil {
		client = NewClient()
	}
	request, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return Response{}, fmt.Errorf("create HTTP request: %w", err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return Response{}, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	reader := io.Reader(response.Body)
	if maxBytes > 0 {
		reader = io.LimitReader(response.Body, maxBytes)
	}
	body, err := io.ReadAll(reader)
	result := Response{Status: response.StatusCode, Header: response.Header, Body: body}
	if err != nil {
		return result, fmt.Errorf("read HTTP response: %w", err)
	}
	return result, nil
}

// DecodeJSON unmarshals body into destination, naming the API in errors.
func DecodeJSON(body []byte, destination any, description string) error {
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode %s JSON: %w", description, err)
	}
	return nil
}

// WithQuery appends the encoded query values to rawURL.
func WithQuery(rawURL string, values url.Values) string {
	if encoded := values.Encode(); encoded != "" {
		return rawURL + "?" + encoded
	}
	return rawURL
}

// Join concatenates a base URL and a path with exactly one separator.
func Join(baseURL, path string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

// BasicAuth builds an HTTP basic authorization header value.
func BasicAuth(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

// APIError describes a failed API call, including a truncated response body.
func APIError(service string, status int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	if len(detail) > 500 {
		detail = detail[:500] + "..."
	}
	if detail == "" {
		detail = http.StatusText(status)
	}
	return fmt.Errorf("%s request failed (HTTP %d): %s", service, status, detail)
}

// ParseURL parses an absolute URL and rejects relative or malformed values.
func ParseURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid URL: %s", value)
	}
	return parsed, nil
}
