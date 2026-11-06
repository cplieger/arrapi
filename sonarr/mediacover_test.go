package sonarr

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

func TestMediaCoverURLPrefixPerEngine(t *testing.T) {
	for name, tc := range map[string]struct {
		serve  func(*fake) *fake
		detect []string
		want   string
	}{
		"Sonarr v4 (API v3)": {func(f *fake) *fake { return f.sonarr4(v4) }, detectV4, fakeBase + "/api/v3/mediacover/12/poster-250.jpg"},
		"Sonarr v5 (API v5)": {func(f *fake) *fake { return f.sonarr5(v5) }, detectV5, fakeBase + "/api/v5/mediacover/12/poster-250.jpg"},
	} {
		t.Run(name, func(t *testing.T) {
			f := tc.serve(newFake())
			c := newClient(t, f)
			for call := range 2 {
				got, err := c.MediaCoverURL(t.Context(), 12, "poster-250.jpg")
				if err != nil || got != tc.want {
					t.Fatalf("call %d: MediaCoverURL = (%q, %v), want %q", call, got, err, tc.want)
				}
			}
			wantRequests(t, f, tc.detect...)
		})
	}
}

func TestMediaCoverURLKeepsTheBasePath(t *testing.T) {
	f := newFake()
	f.defV = v4
	f.on("GET /sonarr/api", ans(200, v4, apiBody("v3")))
	f.on("GET /sonarr/api/v3/system/status", ans(200, v4, statusBody("Sonarr", v4)))
	c, err := New(fakeBase+"/sonarr/", testKey, WithHTTPClient(&http.Client{Transport: f}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	got, err := c.MediaCoverURL(t.Context(), 3, "fanart.png")
	if want := fakeBase + "/sonarr/api/v3/mediacover/3/fanart.png"; err != nil || got != want {
		t.Errorf("MediaCoverURL = (%q, %v), want %q", got, err, want)
	}
}

func TestMediaCoverURLRefusesBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		id   int
		file string
	}{
		"a parent segment":  {1, "../x.jpg"},
		"a path":            {1, "a/b.jpg"},
		"a query":           {1, "x.jpg?y"},
		"no name":           {1, ".jpg"},
		"another extension": {1, "poster.svg"},
		"an empty name":     {1, ""},
		"a dot-segment":     {1, "a..jpg"},
		"id 0":              {0, "poster.jpg"},
		"a negative id":     {-4, "poster.jpg"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			c := newClient(t, f)
			got, err := c.MediaCoverURL(t.Context(), tc.id, tc.file)
			if err == nil || got != "" {
				t.Errorf("MediaCoverURL(%d, %q) = (%q, %v), want an error and no URL", tc.id, tc.file, got, err)
			}
			wantRequests(t, f)
		})
	}
}

// MediaCoverURL reads no answer, so after an in-place upgrade it keeps the old API's
// path, sending nothing, until a call that does read one ends that detection.
func TestMediaCoverURLAcrossAnUpgrade(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	f.sonarr5(v5)
	got, err := c.MediaCoverURL(t.Context(), 12, "poster.jpg")
	if want := fakeBase + "/api/v3/mediacover/12/poster.jpg"; err != nil || got != want {
		t.Fatalf("before Info: MediaCoverURL = (%q, %v), want %q", got, err, want)
	}
	wantRequests(t, f)
	if _, err := c.Info(t.Context()); err != nil {
		t.Fatalf("Info: %v", err)
	}
	got, err = c.MediaCoverURL(t.Context(), 12, "poster.jpg")
	if want := fakeBase + "/api/v5/mediacover/12/poster.jpg"; err != nil || got != want {
		t.Fatalf("after Info: MediaCoverURL = (%q, %v), want %q", got, err, want)
	}
}

const jpeg = "\xff\xd8\xff"

func TestMediaCoverReadsTheFilePerEngine(t *testing.T) {
	for name, tc := range map[string]struct {
		serve  func(*fake) *fake
		v      string
		route  string
		detect []string
	}{
		"Sonarr v4 (API v3)": {func(f *fake) *fake { return f.sonarr4(v4) }, v4, "GET /api/v3/mediacover/41/poster-250.jpg", detectV4},
		"Sonarr v5 (API v5)": {func(f *fake) *fake { return f.sonarr5(v5) }, v5, "GET /api/v5/mediacover/41/poster-250.jpg", detectV5},
	} {
		t.Run(name, func(t *testing.T) {
			f := tc.serve(newFake())
			f.on(tc.route, file(200, tc.v, "image/jpeg", jpeg))
			c := newClient(t, f)
			got, found, err := c.MediaCover(t.Context(), 41, "poster-250.jpg", 2<<20)
			if err != nil || !found {
				t.Fatalf("MediaCover = (found %v, %v), want found", found, err)
			}
			if got.ContentType != "image/jpeg" || string(got.Data) != jpeg {
				t.Errorf("MediaCover = %+v, want image/jpeg and the three bytes", got)
			}
			wantRequests(t, f, append(tc.detect, tc.route)...)
		})
	}
}

func TestMediaCoverAnswers(t *testing.T) {
	const route = "GET /api/v3/mediacover/41/poster-250.jpg"
	for name, tc := range map[string]struct {
		check func(*testing.T, error)
		s     step
	}{
		"a missing file": {func(t *testing.T, err error) {
			if err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		}, ans(404, v4, "")},
		"another media type": {func(t *testing.T, err error) {
			if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok {
				t.Errorf("err = %v, want *arrapi.DecodeError", err)
			}
		}, file(200, v4, "text/html", "<html></html>")},
		"over the limit": {func(t *testing.T, err error) {
			if e, ok := errors.AsType[*arrapi.ResponseTooLargeError](err); !ok || e.Limit != 2<<20 {
				t.Errorf("err = %v, want *arrapi.ResponseTooLargeError with Limit 2 MiB", err)
			}
		}, file(200, v4, "image/jpeg", strings.Repeat("x", 2<<20+1))},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			f.on(route, tc.s)
			got, found, err := newClient(t, f).MediaCover(t.Context(), 41, "poster-250.jpg", 2<<20)
			tc.check(t, err)
			if found || got.ContentType != "" || got.Data != nil {
				t.Errorf("MediaCover = (%+v, found %v), want the zero Cover, not found", got, found)
			}
			if n := count(f, route); n != 1 {
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
			f := newFake().sonarr4(v4)
			got, found, err := newClient(t, f).MediaCover(t.Context(), tc.id, tc.file, tc.limit)
			if !errors.Is(err, tc.want) || found || got.Data != nil {
				t.Errorf("MediaCover(%d, %q, %d) = (%+v, %v, %v), want %v", tc.id, tc.file, tc.limit, got, found, err, tc.want)
			}
			wantRequests(t, f)
		})
	}
}

// A read answered by Sonarr 5 on /api/v3 after an in-place upgrade moves to /api/v5.
func TestMediaCoverMovesAcrossAnUpgrade(t *testing.T) {
	const upgraded = "5.0.0.1"
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	f.sonarr5(upgraded)
	f.on("GET /api/v3/mediacover/41/poster.jpg", file(200, upgraded, "image/jpeg", "stale"))
	f.on("GET /api/v5/mediacover/41/poster.jpg", file(200, upgraded, "image/jpeg", jpeg))
	got, found, err := c.MediaCover(t.Context(), 41, "poster.jpg", 1<<20)
	if err != nil || !found || string(got.Data) != jpeg {
		t.Fatalf("MediaCover = (%q, %v, %v), want the bytes from /api/v5", got.Data, found, err)
	}
	wantRequests(t, f, join([]string{"GET /api/v3/mediacover/41/poster.jpg"}, detectV5, []string{"GET /api/v5/mediacover/41/poster.jpg"})...)
}
