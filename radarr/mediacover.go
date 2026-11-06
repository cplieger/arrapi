package radarr

import (
	"context"
	"errors"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/coverfile"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

var (
	errMediaCoverID    = errors.New("arrapi: media cover movie id must be positive")
	errMediaCoverFile  = errors.New("arrapi: media cover file must be one path segment of names joined by dots, ending .jpg, .png or .gif")
	errMediaCoverLimit = errors.New("arrapi: a media cover limit must be 1 byte or more")
)

// MediaCoverURL returns the URL of the movie's cover image file ("poster.jpg",
// "fanart-360.jpg") under the configured base URL; it detects when no detection is
// current and fetches nothing. It reads no answer, so it keeps using a cached
// detection until another call observes an answer that ends it.
func (c *Client) MediaCoverURL(ctx context.Context, movieID int, file string) (string, error) {
	if err := checkMediaCover(movieID, file); err != nil {
		return "", err
	}
	_, e, err := c.s.Current(ctx)
	if err != nil {
		return "", err
	}
	return c.t.BaseURL() + e.mediaCoverPath(movieID, file), nil
}

// MediaCover reads the movie's cover image file ("poster-250.jpg", "fanart.jpg"), the
// file MediaCoverURL names, with the client's key, so the key never leaves the client.
// It accepts the answer only when it carries the media type the file's extension names
// (image/jpeg for .jpg, image/png for .png, image/gif for .gif) and holds 1 to limit
// bytes: another type, or an empty body, is an *arrapi.DecodeError, and a longer body an
// *arrapi.ResponseTooLargeError. It returns (zero, false, nil) when Radarr has no such
// file. A movieID below 1, a file MediaCoverURL refuses, or a limit below 1 is refused
// before any request.
func (c *Client) MediaCover(ctx context.Context, movieID int, file string, limit int64) (arrapi.Cover, bool, error) {
	if err := checkMediaCover(movieID, file); err != nil {
		return arrapi.Cover{}, false, err
	}
	if limit < 1 {
		return arrapi.Cover{}, false, errMediaCoverLimit
	}
	type found struct {
		cover arrapi.Cover
		ok    bool
	}
	f, err := read(ctx, c, func(ctx context.Context, d *transport.Detection, e radarrAPIv3) (found, transport.Result) {
		cover, r := e.mediaCover(ctx, d, movieID, file, limit)
		return found{cover, r.Accepted()}, r
	})
	return f.cover, f.ok, err
}

func checkMediaCover(movieID int, file string) error {
	if movieID < 1 {
		return errMediaCoverID
	}
	if !coverfile.Valid(file) {
		return errMediaCoverFile
	}
	return nil
}
