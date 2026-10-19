package radarr

import (
	"strconv"
	"strings"
)

// WebURL returns the web UI page of the movie under baseURL, {base}/movie/{TMDB id},
// or "" when baseURL is empty or the movie has no TMDB id. Radarr sets a movie's
// title slug to its TMDB id, and the UI route is not API-versioned.
func (m *Movie) WebURL(baseURL string) string {
	if baseURL == "" || m.IDs.TMDB <= 0 {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/movie/" + strconv.Itoa(m.IDs.TMDB)
}
