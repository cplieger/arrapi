package arrapi

import (
	"context"
	"strings"
)

// GetTags returns all tags defined in the Sonarr or Radarr instance.
func (c *client) GetTags(ctx context.Context) ([]Tag, error) {
	return fetchAll[Tag](ctx, c, apiPrefix+"/tag")
}

// TagIDs returns the set of tag IDs whose labels match any of the given labels.
// Matching is case-insensitive and trims surrounding whitespace (Sonarr and
// Radarr store tag labels lowercased). Labels with no matching tag are simply
// absent from the result. It returns nil when no labels are supplied.
func TagIDs(tags []Tag, labels ...string) map[int]struct{} {
	if len(labels) == 0 {
		return nil
	}
	want := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		if norm := strings.ToLower(strings.TrimSpace(l)); norm != "" {
			want[norm] = struct{}{}
		}
	}
	ids := make(map[int]struct{})
	for _, t := range tags {
		if _, ok := want[strings.ToLower(strings.TrimSpace(t.Label))]; ok {
			ids[t.ID] = struct{}{}
		}
	}
	return ids
}

// HasAnyTag reports whether itemTags contains any of the tag IDs in ids. It is
// the companion to TagIDs for include/exclude filtering of series and movies.
func HasAnyTag(itemTags []int, ids map[int]struct{}) bool {
	for _, id := range itemTags {
		if _, ok := ids[id]; ok {
			return true
		}
	}
	return false
}
