package radarr

import "testing"

func TestMovieWebURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
		tmdbID  int
	}{
		{"tmdb id and base joined", "https://radarr.example.com", "https://radarr.example.com/movie/378064", 378064},
		{"trailing slash on base trimmed", "https://radarr.example.com/", "https://radarr.example.com/movie/378064", 378064},
		{"reverse-proxy subpath preserved", "https://host.example.com/radarr", "https://host.example.com/radarr/movie/378064", 378064},
		{"zero tmdb id yields no link", "https://radarr.example.com", "", 0},
		{"negative tmdb id yields no link", "https://radarr.example.com", "", -5},
		{"empty base yields no link", "", "", 378064},
		{"zero tmdb id and empty base yields no link", "", "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Movie{IDs: ExternalIDs{TMDB: tc.tmdbID}}
			if got := m.WebURL(tc.baseURL); got != tc.want {
				t.Errorf("Movie{TMDB:%d}.WebURL(%q) = %q, want %q", tc.tmdbID, tc.baseURL, got, tc.want)
			}
		})
	}
}
