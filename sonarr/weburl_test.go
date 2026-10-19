package sonarr

import "testing"

func TestSeriesWebURL(t *testing.T) {
	tests := []struct {
		name    string
		slug    string
		baseURL string
		want    string
	}{
		{"slug and base joined", "the-show", "https://sonarr.example.com", "https://sonarr.example.com/series/the-show"},
		{"trailing slash on base trimmed", "the-show", "https://sonarr.example.com/", "https://sonarr.example.com/series/the-show"},
		{"reverse-proxy subpath preserved", "the-show", "https://host.example.com/sonarr", "https://host.example.com/sonarr/series/the-show"},
		{"path separator in slug escaped", "foo/bar", "https://sonarr.example.com", "https://sonarr.example.com/series/foo%2Fbar"},
		{"dot-dot slug neutralized", "..", "https://sonarr.example.com", "https://sonarr.example.com/series/%2E%2E"},
		{"single-dot slug neutralized", ".", "https://sonarr.example.com", "https://sonarr.example.com/series/%2E"},
		{"url metacharacters in slug escaped", `a?b#c d"<`, "https://sonarr.example.com", "https://sonarr.example.com/series/a%3Fb%23c%20d%22%3C"},
		{"empty base yields no link", "the-show", "", ""},
		{"empty slug yields no link", "", "https://sonarr.example.com", ""},
		{"empty base and slug yields no link", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := Series{TitleSlug: tc.slug}
			if got := s.WebURL(tc.baseURL); got != tc.want {
				t.Errorf("Series{TitleSlug:%q}.WebURL(%q) = %q, want %q", tc.slug, tc.baseURL, got, tc.want)
			}
		})
	}
}
