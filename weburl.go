package arrapi

import (
	"net/url"
	"strconv"
	"strings"
)

// WebURL returns the Sonarr web-UI deep-link to this series for the given
// Sonarr base URL (e.g. "https://sonarr.example.com"), or "" when the base URL
// or the series title slug is empty. Sonarr's only per-series route is
// /series/{titleSlug}, where the slug is a TheTVDB text slug (set from the
// metadata provider) that is not derivable from any ID, so the slug must come
// from the series record.
func (s *Series) WebURL(baseURL string) string {
	return webURL(baseURL, "series", s.TitleSlug)
}

// WebURL returns the Radarr web-UI deep-link to this movie for the given Radarr
// base URL, or "" when the base URL is empty or the movie has no TMDB id.
// Radarr's per-movie route is /movie/{titleSlug}, and Radarr sets that slug to
// the TMDB id, so the numeric TMDB id resolves the page directly.
func (m *Movie) WebURL(baseURL string) string {
	if m.TmdbID == 0 {
		return ""
	}
	return webURL(baseURL, "movie", strconv.Itoa(m.TmdbID))
}

// webURL joins an arr base URL, a route segment, and an id-or-slug into a
// web-UI deep-link, trimming a trailing slash on the base. It returns "" when
// the base or the id-or-slug is empty, so a caller can treat "" as "no link".
func webURL(baseURL, segment, idOrSlug string) string {
	if baseURL == "" || idOrSlug == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/" + segment + "/" + escapeWebPathSegment(idOrSlug)
}

// escapeWebPathSegment percent-encodes a single path segment for a web-UI
// deep-link. It wraps url.PathEscape, which encodes slash, query, fragment,
// space, and markup characters but intentionally leaves the dot-segments "."
// and ".." unchanged. A hostile arr endpoint can return a title slug of "." or
// ".." that a browser or proxy would normalize to the current or parent path,
// so those two exact segments have their dots percent-encoded as "%2E" to keep
// the slug confined to a literal path segment. Normal slugs and the numeric
// TMDB id pass through url.PathEscape byte-for-byte.
func escapeWebPathSegment(s string) string {
	escaped := url.PathEscape(s)
	if escaped == "." || escaped == ".." {
		return strings.ReplaceAll(escaped, ".", "%2E")
	}
	return escaped
}
