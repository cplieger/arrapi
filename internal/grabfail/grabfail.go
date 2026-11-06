// Package grabfail reads why an arr refused a grab from its status and its own
// sentence, so a proxy's 404 or 409 is never taken for the arr's refusal, and masks
// the magnet links a refusal quotes.
package grabfail

import (
	"errors"
	"net/http"
	"strings"

	"github.com/cplieger/arrapi/v3"
)

// The sentences Sonarr's and Radarr's ReleaseController write for a refused grab
// (Sonarr.Api.V3 and Sonarr.Api.V5 Release, Radarr.Api.V3 Indexers).
const (
	notCached    = "Couldn't find requested release in cache, try searching again"
	unmappedHead = "Unable to "
	unmappedTail = ", will need to be manually provided"
	unavailable  = "Getting release from indexer failed"
)

// Classify masks the Message and Body of the *arrapi.StatusError err holds, in place,
// from their first magnet link to their end, then returns a *arrapi.GrabError over err when err is itself a
// *arrapi.StatusError whose code and trimmed message are an arr's refusal of a grab,
// and err otherwise. A *StatusError wrapped in another error, as a
// *arrapi.ServerChangedError holds one, came from another server version, so it is
// masked but never classified.
func Classify(err error) error {
	if se, ok := errors.AsType[*arrapi.StatusError](err); ok {
		se.Message, se.Body = maskMagnetLinks(se.Message), maskMagnetLinks(se.Body)
	}
	se, ok := err.(*arrapi.StatusError)
	if !ok {
		return err
	}
	if r := reason(se.Code, strings.TrimSpace(se.Message)); r != 0 {
		return &arrapi.GrabError{Err: se, Reason: r}
	}
	return err
}

// Sonarr 5's 409 quotes its download error, and one quotes the release's magnet link,
// whose announce URL can hold a tracker's passkey (NzbDrone.Core TorrentClientBase).
const (
	magnetScheme = "magnet:"
	// No byte of the mask is in an API key's alphabet, so it cannot spell the key the
	// transport already removed, and it is shorter than the scheme, so a capped text
	// stays within its cap.
	magnetMask = "[***]"
	cutMarker  = "..."
)

// maskMagnetLinks replaces s from its first magnet scheme, in any case (RFC 3986
// section 3.1), to its end with magnetMask, keeping a trailing cutMarker, the mark of a
// text the transport cut. No byte ends the mask early: the quoted link is one
// MagnetLink.Parse refused, so it can hold a space, a quote or a decoded line break.
func maskMagnetLinks(s string) string {
	i := indexFold(s, magnetScheme)
	if i < 0 {
		return s
	}
	if strings.HasSuffix(s, cutMarker) && len(s)-i-len(cutMarker) >= len(magnetScheme) {
		return s[:i] + magnetMask + cutMarker
	}
	return s[:i] + magnetMask
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func reason(code int, msg string) arrapi.GrabReason {
	switch {
	case code == http.StatusNotFound && msg == notCached:
		return arrapi.GrabNotCached
	case code == http.StatusNotFound && strings.HasPrefix(msg, unmappedHead) && strings.HasSuffix(msg, unmappedTail):
		return arrapi.GrabUnmapped
	case code == http.StatusConflict && (msg == unavailable || strings.HasPrefix(msg, unavailable+": ")):
		return arrapi.GrabUnavailable
	}
	return 0
}
