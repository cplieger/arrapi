package arrapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
)

func TestGetSeriesByID(t *testing.T) {
	rs := newServer(t, http.StatusOK, `{"id":42,"title":"Frieren","tvdbId":424536,"year":2023,"monitored":true}`)
	s := fastSonarr(t, rs.srv.URL)

	series, err := s.GetSeriesByID(t.Context(), 42)
	if err != nil {
		t.Fatalf("GetSeriesByID: %v", err)
	}
	if series.ID != 42 || series.Title != "Frieren" || series.TvdbID != 424536 {
		t.Errorf("series = %+v, want id 42 Frieren tvdb 424536", series)
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/series/42?" {
		t.Errorf("path = %q, want /api/v3/series/42?", got)
	}
}

func TestGetSeriesByID_notFound(t *testing.T) {
	rs := newServer(t, http.StatusNotFound, `{"message":"NotFound"}`)
	s := fastSonarr(t, rs.srv.URL)

	if _, err := s.GetSeriesByID(t.Context(), 999); !arrapi.IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false, want true", err)
	}
}

func TestGetEpisodeByID(t *testing.T) {
	rs := newServer(t, http.StatusOK, `{"id":10,"seriesId":7,"seasonNumber":1,"episodeNumber":1,"hasFile":true}`)
	s := fastSonarr(t, rs.srv.URL)

	ep, err := s.GetEpisodeByID(t.Context(), 10)
	if err != nil {
		t.Fatalf("GetEpisodeByID: %v", err)
	}
	if ep.ID != 10 || ep.SeriesID != 7 || !ep.HasFile {
		t.Errorf("episode = %+v, want id 10 series 7 hasFile", ep)
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/episode/10?" {
		t.Errorf("path = %q, want /api/v3/episode/10?", got)
	}
}

func TestGetMovieByID(t *testing.T) {
	rs := newServer(t, http.StatusOK, `{"id":100,"title":"A Silent Voice","tmdbId":378064,"year":2016,"hasFile":true}`)
	r, err := arrapi.NewRadarr(rs.srv.URL, testKey, arrapi.WithBaseDelay(time.Millisecond))
	if err != nil {
		t.Fatalf("NewRadarr: %v", err)
	}
	t.Cleanup(r.Close)

	movie, err := r.GetMovieByID(t.Context(), 100)
	if err != nil {
		t.Fatalf("GetMovieByID: %v", err)
	}
	if movie.ID != 100 || movie.TmdbID != 378064 || !movie.HasFile {
		t.Errorf("movie = %+v, want id 100 tmdb 378064 hasFile", movie)
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/movie/100?" {
		t.Errorf("path = %q, want /api/v3/movie/100?", got)
	}
}

func TestGetMovieByID_notFound(t *testing.T) {
	rs := newServer(t, http.StatusNotFound, `{"message":"NotFound"}`)
	r, err := arrapi.NewRadarr(rs.srv.URL, testKey, arrapi.WithBaseDelay(time.Millisecond))
	if err != nil {
		t.Fatalf("NewRadarr: %v", err)
	}
	t.Cleanup(r.Close)

	if _, err := r.GetMovieByID(t.Context(), 999); !arrapi.IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false, want true", err)
	}
}
