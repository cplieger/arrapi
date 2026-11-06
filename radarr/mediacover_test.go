package radarr

import (
	"errors"
	"net/http"
	"strings"
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

const jpeg = "\xff\xd8\xff"

const coverRoute = "GET /api/v3/mediacover/41/poster-250.jpg"

func TestMediaCoverReadsTheFile(t *testing.T) {
	f := newFake().radarr(vRadarr)
	f.on(coverRoute, file(200, vRadarr, "image/jpeg", jpeg))
	got, found, err := newClient(t, f).MediaCover(t.Context(), 41, "poster-250.jpg", 2<<20)
	if err != nil || !found {
		t.Fatalf("MediaCover = (found %v, %v), want found", found, err)
	}
	if got.ContentType != "image/jpeg" || string(got.Data) != jpeg {
		t.Errorf("MediaCover = %+v, want image/jpeg and the three bytes", got)
	}
	wantRequests(t, f, append(detectRadarr, coverRoute)...)
}

func TestMediaCoverAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		check func(*testing.T, error)
		s     step
	}{
		"a missing file": {func(t *testing.T, err error) {
			if err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		}, ans(404, vRadarr, "")},
		"another media type": {func(t *testing.T, err error) {
			if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok {
				t.Errorf("err = %v, want *arrapi.DecodeError", err)
			}
		}, file(200, vRadarr, "text/html", "<html></html>")},
		"over the limit": {func(t *testing.T, err error) {
			if e, ok := errors.AsType[*arrapi.ResponseTooLargeError](err); !ok || e.Limit != 2<<20 {
				t.Errorf("err = %v, want *arrapi.ResponseTooLargeError with Limit 2 MiB", err)
			}
		}, file(200, vRadarr, "image/jpeg", strings.Repeat("x", 2<<20+1))},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			f.on(coverRoute, tc.s)
			got, found, err := newClient(t, f).MediaCover(t.Context(), 41, "poster-250.jpg", 2<<20)
			tc.check(t, err)
			if found || got.ContentType != "" || got.Data != nil {
				t.Errorf("MediaCover = (%+v, found %v), want the zero Cover, not found", got, found)
			}
			if n := count(f, coverRoute); n != 1 {
				t.Errorf("sent %d cover reads, want 1", n)
			}
		})
	}
}

func TestMediaCoverRefusesBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		want  error
		file  string
		id    int
		limit int64
	}{
		"id 0":              {errMediaCoverID, "poster.jpg", 0, 1},
		"a parent segment":  {errMediaCoverFile, "../x.jpg", 41, 1},
		"another extension": {errMediaCoverFile, "poster.webp", 41, 1},
		"limit 0":           {errMediaCoverLimit, "poster.jpg", 41, 0},
		"a negative limit":  {errMediaCoverLimit, "poster.jpg", 41, -1},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().radarr(vRadarr)
			got, found, err := newClient(t, f).MediaCover(t.Context(), tc.id, tc.file, tc.limit)
			if !errors.Is(err, tc.want) || found || got.Data != nil {
				t.Errorf("MediaCover(%d, %q, %d) = (%+v, %v, %v), want %v", tc.id, tc.file, tc.limit, got, found, err, tc.want)
			}
			wantRequests(t, f)
		})
	}
}
