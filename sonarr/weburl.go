package sonarr

import (
	"net/url"
	"strings"
)

// WebURL returns the web UI page of the series under baseURL, {base}/series/{titleSlug}
// with the slug path-escaped as one segment, or "" when baseURL or the slug is empty.
// The slug is TheTVDB's text slug, derivable from no id, and the UI route is not
// API-versioned.
func (s *Series) WebURL(baseURL string) string {
	if baseURL == "" || s.TitleSlug == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/series/" + escapeSegment(s.TitleSlug)
}

// escapeSegment keeps an untrusted slug one literal path segment: url.PathEscape leaves
// "." and "..", which a browser or proxy would resolve as dot-segments.
func escapeSegment(s string) string {
	escaped := url.PathEscape(s)
	if escaped == "." || escaped == ".." {
		return strings.ReplaceAll(escaped, ".", "%2E")
	}
	return escaped
}
