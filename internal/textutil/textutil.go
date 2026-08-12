// Package textutil normalises the markup-heavy text returned by the
// development tool APIs into compact plain text.
package textutil

import (
	"html"
	"regexp"
	"strings"
)

var markup = regexp.MustCompile(`<[^>]+>`)
var blockMarkup = regexp.MustCompile(`(?i)<(?:br|hr)\b[^>]*>|</?(?:address|article|aside|blockquote|div|dl|dt|dd|fieldset|figcaption|figure|footer|form|h[1-6]|header|li|main|nav|ol|p|pre|section|table|tbody|thead|tfoot|tr|td|th|ul)\b[^>]*>`)

// StripMarkup removes HTML tags and resolves HTML entities.
func StripMarkup(value string) string {
	return html.UnescapeString(markup.ReplaceAllString(value, ""))
}

// Clean strips markup, drops blank lines, and truncates to limit bytes.
func Clean(value string, limit int) string {
	if value == "" {
		return ""
	}
	lines := make([]string, 0)
	for line := range strings.SplitSeq(StripMarkup(value), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return Truncate(strings.Join(lines, "\n"), limit)
}

// CleanHTML converts HTML block boundaries to line breaks before cleaning.
func CleanHTML(value string, limit int) string {
	return Clean(blockMarkup.ReplaceAllString(value, "\n"), limit)
}

// Truncate shortens value to limit bytes, marking cut text with an ellipsis.
func Truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.TrimRight(value[:limit-3], " \t\n\r") + "..."
}
