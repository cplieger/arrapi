package coverfile_test

import (
	"testing"

	"github.com/cplieger/arrapi/v3/internal/coverfile"
)

func TestValid(t *testing.T) {
	for file, want := range map[string]bool{
		"poster.jpg":        true,
		"poster-250.jpg":    true,
		"fanart_360.png":    true,
		"a.b.gif":           true,
		"clearlogo.png":     true,
		"../x.jpg":          false,
		"a/b.jpg":           false,
		"x.jpg?y":           false,
		".jpg":              false,
		"poster.svg":        false,
		"poster.webp":       false,
		"poster.JPG":        false,
		"":                  false,
		"a..jpg":            false,
		"poster.jpg/":       false,
		"poster.jpg\n":      false,
		"poster%2e.jpg":     false,
		"poster.jpg#x":      false,
		"pöster.jpg":        false,
		"poster .jpg":       false,
		"poster.jpg.":       false,
		"poster.jpgx":       false,
		"-.jpg":             true,
		"poster.jpg.backup": false,
	} {
		t.Run(file, func(t *testing.T) {
			if got := coverfile.Valid(file); got != want {
				t.Errorf("Valid(%q) = %v, want %v", file, got, want)
			}
		})
	}
}

func TestMediaType(t *testing.T) {
	for file, want := range map[string]string{
		"poster.jpg":     "image/jpeg",
		"poster-250.jpg": "image/jpeg",
		"clearlogo.png":  "image/png",
		"x.gif":          "image/gif",
		"poster.webp":    "",
		"poster.JPG":     "",
		"../x.jpg":       "",
		"":               "",
	} {
		t.Run(file, func(t *testing.T) {
			if got := coverfile.MediaType(file); got != want {
				t.Errorf("MediaType(%q) = %q, want %q", file, got, want)
			}
		})
	}
}
