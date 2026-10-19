package transport

import (
	"strings"

	"github.com/cplieger/runesafe/v3"
)

// redactedMask replaces each run of bytes a key occurrence covers, in Body and in
// every presented text alike.
const redactedMask = "[redacted]"

// mask writes one redactedMask per run of bytes that an occurrence of the key covers,
// overlapping occurrences included, so a mask written for one cannot leave a fragment
// of another. The key is ASCII (validate), so a run never splits a rune.
func (c *Client) mask(s string) string {
	hit := c.occurrences(s)
	if hit == nil {
		return s
	}
	var b strings.Builder
	for i := range len(s) {
		switch {
		case !hit[i]:
			b.WriteByte(s[i])
		case i == 0 || !hit[i-1]:
			b.WriteString(redactedMask)
		}
	}
	return b.String()
}

// occurrences flags every byte of text an occurrence of the key covers, or returns nil
// when the key does not occur.
func (c *Client) occurrences(text string) []bool {
	key := string(c.key)
	var hit []bool
	for from := 0; ; from++ {
		i := strings.Index(text[from:], key)
		if i < 0 {
			return hit
		}
		if hit == nil {
			hit = make([]bool, len(text))
		}
		from += i
		for k := from; k < from+len(key); k++ {
			hit[k] = true
		}
	}
}

// purge removes the key until s holds none: the mask, the cut marker and their
// neighbours can spell an accepted key ("redacted", "."), and a removal can join two
// fragments into another. s only shrinks, so it ends.
func (c *Client) purge(s string) string {
	for c.containsKey(s) {
		s = strings.ReplaceAll(s, string(c.key), "")
	}
	return s
}

// ContainsKey reports whether s holds the client's key as sent, the only form the
// encoders in play write it in (validate).
//
//deadset:ignore DS1004 -- read only by the fixture recorder, internal/fixturegen and the sonarr and radarr record tests
func (c *Client) ContainsKey(s string) bool { return c.containsKey(s) }

func (c *Client) containsKey(s string) bool { return strings.Contains(s, string(c.key)) }

// Present is the bounded single-line presentation of server text, for an error, a
// Detection, Info or a log attribute: at most limit bytes, holding no occurrence of the
// client's key. It masks with the marker Body uses (redactErrorBody owns Body's
// multi-line form) and caps last, so a key straddling the cap is already gone.
func (c *Client) Present(s string, limit int) string {
	text, _ := runesafe.RedactSingleLineCapped(s, limit, cutMarker, string(c.key))
	return c.purge(text)
}
