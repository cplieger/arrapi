package joinedmedia_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3/internal/joinedmedia"
)

// FuzzLanguages joins two fuzzed halves with "/" and checks the result against
// each half's, so the concatenation property holds without restating the rule.
func FuzzLanguages(f *testing.F) {
	for _, seed := range [][2]string{
		{"", ""},
		{"und", "jpn"},
		{"jpn/eng", " ENG "},
		{" JPN / und ", "eng//jpn"},
		{"Japanese / English", ""},
		{"\u00a0und\u00a0", "x"},
		{"jpn,eng", "und"},
		{"\xff", "\xfe/"},
		{"ǅ", "İ"},
		{strings.Repeat("a/", 200), "b"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		joined := a + "/" + b
		got := joinedmedia.Languages(joined)
		if want := append(joinedmedia.Languages(a), joinedmedia.Languages(b)...); !slices.Equal(got, want) {
			t.Fatalf("Languages(%q) = %q, want Languages(%q) then Languages(%q) = %q", joined, got, a, b, want)
		}
		if limit := strings.Count(joined, "/") + 1; len(got) > limit {
			t.Errorf("Languages(%q) returned %d entries, more than the input's %d tokens", joined, len(got), limit)
		}
		if sentinel := joinedmedia.Languages(joined + "/zxx"); len(sentinel) != len(got)+1 || sentinel[len(sentinel)-1] != "zxx" {
			t.Errorf("Languages(%q + \"/zxx\") = %q, want %q then zxx", joined, sentinel, got)
		}
		lower := strings.ToLower(joined)
		for i, lang := range got {
			if lang == "" || lang == "und" || strings.Contains(lang, "/") {
				t.Errorf("Languages(%q)[%d] = %q: empty, und or not one token", joined, i, lang)
			}
			if lang != strings.ToLower(lang) || lang != strings.TrimSpace(lang) || !strings.Contains(lower, lang) {
				t.Errorf("Languages(%q)[%d] = %q is not a trimmed, lowercased token of the input", joined, i, lang)
			}
		}
	})
}
