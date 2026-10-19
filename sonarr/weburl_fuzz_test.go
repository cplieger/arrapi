package sonarr

import (
	"net/url"
	"strings"
	"testing"
)

// FuzzSeriesWebURL_slugConfinedToOneSegment: the escaped slug stays one path segment,
// is never a bare dot-segment, and percent-decodes back to the slug.
func FuzzSeriesWebURL_slugConfinedToOneSegment(f *testing.F) {
	for _, seed := range []string{
		"the-show", ".", "..", "foo/bar", `a?b#c d"<`,
		"../../etc/passwd", "%2e%2e", "..%2f", "50%", "a+b", "",
	} {
		f.Add(seed)
	}
	const base = "https://sonarr.example.com"
	f.Fuzz(func(t *testing.T, slug string) {
		s := Series{TitleSlug: slug}
		got := s.WebURL(base)
		if slug == "" {
			if got != "" {
				t.Errorf("WebURL with an empty slug = %q, want \"\"", got)
			}
			return
		}
		const prefix = base + "/series/"
		if !strings.HasPrefix(got, prefix) {
			t.Fatalf("WebURL(%q) = %q, want prefix %q", slug, got, prefix)
		}
		seg := strings.TrimPrefix(got, prefix)
		if strings.Contains(seg, "/") {
			t.Errorf("slug %q produced segment %q with an unescaped '/'", slug, seg)
		}
		if seg == "." || seg == ".." {
			t.Errorf("slug %q produced dot-segment %q", slug, seg)
		}
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			t.Fatalf("segment %q is not valid percent-encoding: %v", seg, err)
		}
		if decoded != slug {
			t.Errorf("PathUnescape(%q) = %q, want %q", seg, decoded, slug)
		}
	})
}
