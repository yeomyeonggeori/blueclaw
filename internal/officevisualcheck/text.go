package officevisualcheck

import (
	"html"
	"regexp"
	"strings"
)

var (
	notesPattern = regexp.MustCompile(`(?is)<aside\b[^>]*class="[^"]*\bnotes\b[^"]*"[^>]*>.*?</aside>`)
	tagPattern   = regexp.MustCompile(`(?s)<[^>]*>`)
)

func visibleText(section string) string {
	withoutNotes := notesPattern.ReplaceAllString(section, " ")
	withoutTags := tagPattern.ReplaceAllString(withoutNotes, " ")
	return strings.Join(strings.Fields(html.UnescapeString(withoutTags)), " ")
}
