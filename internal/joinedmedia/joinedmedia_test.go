package joinedmedia_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3/internal/joinedmedia"
)

func TestLanguages(t *testing.T) {
	hundred := strings.Repeat("jpn/eng/", 50)
	var hundredWant []string
	for range 50 {
		hundredWant = append(hundredWant, "jpn", "eng")
	}
	tests := []struct {
		name   string
		joined string
		want   []string
	}{
		{"tokens in order", "jpn/eng/fre", []string{"jpn", "eng", "fre"}},
		{"trimmed", " jpn / eng\t", []string{"jpn", "eng"}},
		{"lowercased", "JPN/Eng", []string{"jpn", "eng"}},
		{"und dropped in any case", "jpn/und/UND/ Und /eng", []string{"jpn", "eng"}},
		{"empty tokens dropped", "/jpn//eng/", []string{"jpn", "eng"}},
		{"whitespace-only tokens dropped", "jpn/  /eng", []string{"jpn", "eng"}},
		{"duplicates kept", "jpn/eng/jpn", []string{"jpn", "eng", "jpn"}},
		{"comma is not a separator", "jpn,eng", []string{"jpn,eng"}},
		{"server names kept as written", "Japanese / English", []string{"japanese", "english"}},
		{"one token", "eng", []string{"eng"}},
		{"empty string is nil", "", nil},
		{"only und is nil", "und", nil},
		{"only separators is nil", "///", nil},
		{"100 tokens all kept", strings.TrimSuffix(hundred, "/"), hundredWant},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := joinedmedia.Languages(tc.joined)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("Languages(%q) = %q (nil %v), want nil %v", tc.joined, got, got == nil, tc.want == nil)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Languages(%q) = %q, want %q", tc.joined, got, tc.want)
			}
		})
	}
}

func TestLanguage(t *testing.T) {
	tests := []struct {
		name string
		tok  string
		want string
		ok   bool
	}{
		{"kept", "jpn", "jpn", true},
		{"trimmed and lowercased", " ENG\t", "eng", true},
		{"no separator inside a token", "jpn|eng", "jpn|eng", true},
		{"und refused", "und", "und", false},
		{"und refused in any case", " Und ", "und", false},
		{"empty refused", "", "", false},
		{"whitespace refused", "  ", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := joinedmedia.Language(tc.tok)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Errorf("Language(%q) = %q, %v, want %q, %v", tc.tok, got, ok, tc.want, tc.ok)
			}
		})
	}
}
