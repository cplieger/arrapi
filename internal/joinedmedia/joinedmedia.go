// Package joinedmedia maps the audio-language string both API v3 wires send, Sonarr v4
// (API v3) and Radarr (API v3), to a language list. Each server joins the file's
// languages with "/" (Sonarr v4-develop src/Sonarr.Api.V3/EpisodeFiles/MediaInfoResource.cs
// and Radarr develop src/Radarr.Api.V3/MovieFiles/MediaInfoResource.cs, ConcatToString("/")).
// Language owns the per-token rule every engine applies.
package joinedmedia

import "strings"

// Languages returns the "/"-separated tokens of joined in order, each through Language,
// duplicates kept; nil when none is left.
func Languages(joined string) []string {
	var langs []string
	for tok := range strings.SplitSeq(joined, "/") {
		if lang, ok := Language(tok); ok {
			langs = append(langs, lang)
		}
	}
	return langs
}

// Language returns tok trimmed and lowercased, and false when that is "" or "und".
func Language(tok string) (string, bool) {
	tok = strings.ToLower(strings.TrimSpace(tok))
	return tok, tok != "" && tok != "und"
}
