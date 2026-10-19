package sonarr

import (
	"net/http"
	"testing"
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
