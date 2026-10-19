package sonarr

import (
	"context"
	"errors"
	"regexp"
)

var (
	errMediaCoverID   = errors.New("arrapi: media cover series id must be positive")
	errMediaCoverFile = errors.New("arrapi: media cover file must be one path segment of names joined by dots, ending .jpg, .png or .gif")
)

// mediaCoverFile narrows Sonarr's route constraint (.+)\.(jpg|png|gif) to one segment
// with no dot-segment, so a caller's name cannot add a path or a query.
var mediaCoverFile = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*\.(jpg|png|gif)$`)

// MediaCoverURL returns the URL of the series' cover image file ("poster.jpg",
// "fanart-360.jpg") under the configured base URL, for the API of the current
// detection; it detects when none is current and fetches nothing. After an in-place
// upgrade it keeps naming the old API's path until another call observes an answer
// from the new version, which ends that detection; Info and RescanSeries detect
// fresh, so either one does.
func (c *Client) MediaCoverURL(ctx context.Context, seriesID int, file string) (string, error) {
	if seriesID < 1 {
		return "", errMediaCoverID
	}
	if !mediaCoverFile.MatchString(file) {
		return "", errMediaCoverFile
	}
	_, e, err := c.s.Current(ctx)
	if err != nil {
		return "", err
	}
	return c.t.BaseURL() + e.mediaCoverPath(seriesID, file), nil
}
