package radarr

import (
	"context"
	"errors"
	"regexp"
)

var (
	errMediaCoverID   = errors.New("arrapi: media cover movie id must be positive")
	errMediaCoverFile = errors.New("arrapi: media cover file must be one path segment of names joined by dots, ending .jpg, .png or .gif")
)

// mediaCoverFile narrows Radarr's route constraint (.+)\.(jpg|png|gif) to one segment
// with no dot-segment, so a caller's name cannot add a path or a query.
var mediaCoverFile = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*\.(jpg|png|gif)$`)

// MediaCoverURL returns the URL of the movie's cover image file ("poster.jpg",
// "fanart-360.jpg") under the configured base URL; it detects when no detection is
// current and fetches nothing. It reads no answer, so it keeps using a cached
// detection until another call observes an answer that ends it.
func (c *Client) MediaCoverURL(ctx context.Context, movieID int, file string) (string, error) {
	if movieID < 1 {
		return "", errMediaCoverID
	}
	if !mediaCoverFile.MatchString(file) {
		return "", errMediaCoverFile
	}
	_, e, err := c.s.Current(ctx)
	if err != nil {
		return "", err
	}
	return c.t.BaseURL() + e.mediaCoverPath(movieID, file), nil
}
