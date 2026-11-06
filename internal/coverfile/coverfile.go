// Package coverfile is the rule both servers' media cover route shares: which file
// names a caller may ask for, and which media type each is served as.
package coverfile

import (
	"regexp"
	"strings"
)

var name = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*\.(jpg|png|gif)$`)

// Valid reports whether file is one path segment of names joined by dots, ending
// .jpg, .png or .gif: the route constraint (.+)\.(jpg|png|gif) narrowed so a caller's
// name cannot add a path or a query.
func Valid(file string) bool { return name.MatchString(file) }

// MediaType is the type the server sends for a Valid file, by its extension:
// "image/jpeg", "image/png" or "image/gif"; "" for a file Valid refuses.
func MediaType(file string) string {
	if !Valid(file) {
		return ""
	}
	switch file[strings.LastIndexByte(file, '.'):] {
	case ".jpg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	}
	return "image/gif"
}
