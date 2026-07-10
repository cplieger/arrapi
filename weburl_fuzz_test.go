package arrapi_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/cplieger/arrapi"
)

// FuzzSeriesWebURL_slugConfinedToOneSegment exercises the untrusted-slug
// sanitizer (escapeWebPathSegment, reached through Series.WebURL) with three
// security invariants: the escaped slug stays a single path segment (no
// unescaped '/'), it is never a bare "." or ".." segment a browser or proxy
// would normalize into a traversal, and the escaping is lossless (percent-
// decoding the emitted segment yields the original slug).
func FuzzSeriesWebURL_slugConfinedToOneSegment(f *testing.F) {
	for _, seed := range []string{
		"the-show", ".", "..", "foo/bar", `a?b#c d"<`,
		"../../etc/passwd", "%2e%2e", "..%2f", "50%", "a+b", "",
	} {
		f.Add(seed)
	}
	const base = "https://sonarr.example.com"
	f.Fuzz(func(t *testing.T, slug string) {
		s := arrapi.Series{TitleSlug: slug}
		got := s.WebURL(base)
		if slug == "" {
			if got != "" {
				t.Errorf("Series{TitleSlug:%q}.WebURL(%q) = %q, want %q", slug, base, got, "")
			}
			return
		}
		const prefix = base + "/series/"
		if !strings.HasPrefix(got, prefix) {
			t.Fatalf("Series{TitleSlug:%q}.WebURL(%q) = %q, want prefix %q", slug, base, got, prefix)
		}
		seg := strings.TrimPrefix(got, prefix)
		if strings.Contains(seg, "/") {
			t.Errorf("slug %q produced segment %q with an unescaped '/'", slug, seg)
		}
		if seg == "." || seg == ".." {
			t.Errorf("slug %q produced dot-segment %q a browser would normalize", slug, seg)
		}
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			t.Fatalf("segment %q is not valid percent-encoding: %v", seg, err)
		}
		if decoded != slug {
			t.Errorf("round-trip: PathUnescape(%q) = %q, want %q", seg, decoded, slug)
		}
	})
}
