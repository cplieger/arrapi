package transport

import (
	"strings"

	"github.com/cplieger/runesafe/v3"
)

const cutMarker = "..."

// cutMarked marks s as cut: it keeps at most limit-3 bytes on a rune boundary, trims
// any trailing proper prefix of the key, and appends the marker, so the result is at
// most limit bytes. The trim sits before the marker, which would hide the prefix from
// it; the purge comes after it, because the key can be the marker.
func (c *Client) cutMarked(s string, limit int) string {
	return c.purge(c.trimTrailingKeyPrefix(runesafe.CapBytes(s, max(limit-len(cutMarker), 0))) + cutMarker)
}

// trimTrailingKeyPrefix removes from s every trailing run that is a non-empty proper
// prefix of the key. Redaction matches only whole occurrences, so a cut through a key
// leaves such a prefix as the tail.
func (c *Client) trimTrailingKeyPrefix(s string) string {
	key := string(c.key)
	for {
		k := min(len(key)-1, len(s))
		for k >= 1 && !strings.HasSuffix(s, key[:k]) {
			k--
		}
		if k < 1 {
			return s
		}
		s = s[:len(s)-k]
	}
}
