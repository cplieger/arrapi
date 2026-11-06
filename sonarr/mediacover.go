package sonarr

import (
	"context"
	"errors"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/coverfile"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

var (
	errMediaCoverID    = errors.New("arrapi: media cover series id must be positive")
	errMediaCoverFile  = errors.New("arrapi: media cover file must be one path segment of names joined by dots, ending .jpg, .png or .gif")
	errMediaCoverLimit = errors.New("arrapi: a media cover limit must be 1 byte or more")
)

// MediaCoverURL returns the URL of the series' cover image file ("poster.jpg",
// "fanart-360.jpg") under the configured base URL, for the API of the current
// detection; it detects when none is current and fetches nothing. After an in-place
// upgrade it keeps naming the old API's path until another call observes an answer
// from the new version, which ends that detection; Info and RescanSeries detect
// fresh, so either one does.
func (c *Client) MediaCoverURL(ctx context.Context, seriesID int, file string) (string, error) {
	if err := checkMediaCover(seriesID, file); err != nil {
		return "", err
	}
	_, e, err := c.s.Current(ctx)
	if err != nil {
		return "", err
	}
	return c.t.BaseURL() + e.mediaCoverPath(seriesID, file), nil
}

// MediaCover reads the series' cover image file ("poster-250.jpg", "fanart.jpg"), the
// file MediaCoverURL names, with the client's key, so the key never leaves the client.
// It accepts the answer only when it carries the media type the file's extension names
// (image/jpeg for .jpg, image/png for .png, image/gif for .gif) and holds 1 to limit
// bytes: another type, or an empty body, is an *arrapi.DecodeError, and a longer body an
// *arrapi.ResponseTooLargeError. It returns (zero, false, nil) when Sonarr has no such
// file. A seriesID below 1, a file MediaCoverURL refuses, or a limit below 1 is refused
// before any request.
func (c *Client) MediaCover(ctx context.Context, seriesID int, file string, limit int64) (arrapi.Cover, bool, error) {
	if err := checkMediaCover(seriesID, file); err != nil {
		return arrapi.Cover{}, false, err
	}
	if limit < 1 {
		return arrapi.Cover{}, false, errMediaCoverLimit
	}
	type found struct {
		cover arrapi.Cover
		ok    bool
	}
	f, err := read(ctx, c.s.Read, func(ctx context.Context, d *transport.Detection, e engine) (found, transport.Result) {
		cover, r := e.mediaCover(ctx, d, seriesID, file, limit)
		return found{cover, r.Accepted()}, r
	})
	return f.cover, f.ok, err
}

func checkMediaCover(seriesID int, file string) error {
	if seriesID < 1 {
		return errMediaCoverID
	}
	if !coverfile.Valid(file) {
		return errMediaCoverFile
	}
	return nil
}
