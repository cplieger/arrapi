package radarr

import (
	"net/http"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

func TestMediaCoverURL(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	for call := range 2 {
		got, err := c.MediaCoverURL(t.Context(), 12, "poster-250.jpg")
		if want := fakeBase + "/api/v3/mediacover/12/poster-250.jpg"; err != nil || got != want {
			t.Fatalf("call %d: MediaCoverURL = (%q, %v), want %q", call, got, err, want)
		}
	}
	wantRequests(t, f, detectRadarr...)
}

func TestMediaCoverURLKeepsTheBasePath(t *testing.T) {
	f := newFake()
	f.defV = vRadarr
	f.on("GET /radarr/api", ans(200, vRadarr, apiBody("v3")))
	f.on("GET /radarr/api/v3/system/status", ans(200, vRadarr, statusBody("Radarr", vRadarr)))
	c, err := New(fakeBase+"/radarr/", testKey, WithHTTPClient(&http.Client{Transport: f}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	got, err := c.MediaCoverURL(t.Context(), 3, "fanart.png")
	if want := fakeBase + "/radarr/api/v3/mediacover/3/fanart.png"; err != nil || got != want {
		t.Errorf("MediaCoverURL = (%q, %v), want %q", got, err, want)
	}
}

func TestMediaCoverURLRefusesBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		id   int
	}{
		"a parent segment":  {"../x.jpg", 1},
		"a path":            {"a/b.jpg", 1},
		"a query":           {"x.jpg?y", 1},
		"no name":           {".jpg", 1},
		"another extension": {"poster.svg", 1},
		"an empty name":     {"", 1},
		"a dot-segment":     {"a..jpg", 1},
		"id 0":              {"poster.jpg", 0},
		"a negative id":     {"poster.jpg", -4},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			c := newClient(t, f)
			got, err := c.MediaCoverURL(t.Context(), tc.id, tc.file)
			if err == nil || got != "" {
				t.Errorf("MediaCoverURL(%d, %q) = (%q, %v), want an error and no URL", tc.id, tc.file, got, err)
			}
			wantRequests(t, f)
		})
	}
}

func TestMediaCoverURLDetectionFailureIsReturned(t *testing.T) {
	f := newFake().sonarr4(v4Sonarr)
	got, err := newClient(t, f).MediaCoverURL(t.Context(), 12, "poster.jpg")
	if got != "" {
		t.Errorf("MediaCoverURL = %q, want no URL from a server that is not Radarr", got)
	}
	wantUnsupported(t, err, arrapi.ReasonWrongApp)
}

// MediaCoverURL reads no answer: after a re-point to a Sonarr it keeps the Radarr
// detection, sending nothing, and a same-version Info leaves it, until a call answered
// by the Sonarr ends it.
func TestMediaCoverURLAcrossARepoint(t *testing.T) {
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	want := fakeBase + "/api/v3/mediacover/12/poster.jpg"
	if _, err := c.Info(t.Context()); err != nil {
		t.Fatalf("Info: %v", err)
	}
	if got, err := c.MediaCoverURL(t.Context(), 12, "poster.jpg"); err != nil || got != want {
		t.Fatalf("after a same-version Info: MediaCoverURL = (%q, %v), want %q", got, err, want)
	}
	f.sonarr4(v4Sonarr)
	f.clearLog()
	if got, err := c.MediaCoverURL(t.Context(), 12, "poster.jpg"); err != nil || got != want {
		t.Fatalf("after the re-point: MediaCoverURL = (%q, %v), want %q", got, err, want)
	}
	wantRequests(t, f)
	if _, err := c.Tags(t.Context()); err == nil {
		t.Fatal("Tags accepted the Sonarr's answer")
	}
	got, err := c.MediaCoverURL(t.Context(), 12, "poster.jpg")
	if got != "" {
		t.Fatalf("after the Sonarr answered: MediaCoverURL = %q", got)
	}
	wantUnsupported(t, err, arrapi.ReasonWrongApp)
}
