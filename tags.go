package arrapi

import (
	"context"
	"strings"
)

// GetTags returns all tags defined in the Sonarr or Radarr instance.
func (c *client) GetTags(ctx context.Context) ([]Tag, error) {
	return fetchAll[Tag](ctx, c, apiPrefix+"/tag")
}

// ResolveTagIDs fetches the instance's tags and resolves the given labels to
// their tag IDs. It returns the set of matched IDs and, separately, the labels
// that matched no tag, so a caller filtering by configured tag names can flag a
// misconfiguration rather than silently ignore it. Matching is case-insensitive
// and trims surrounding whitespace. Passing no labels returns (nil, nil, nil)
// without issuing a request. It is the network-backed convenience over
// GetTags + TagIDs + UnmatchedLabels. Available on both Sonarr and Radarr.
func (c *client) ResolveTagIDs(ctx context.Context, labels ...string) (ids map[int]struct{}, unmatched []string, err error) {
	if len(labels) == 0 {
		return nil, nil, nil
	}
	tags, err := c.GetTags(ctx)
	if err != nil {
		return nil, nil, err
	}
	return TagIDs(tags, labels...), UnmatchedLabels(tags, labels...), nil
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
		if norm := normalizeLabel(l); norm != "" {
			want[norm] = struct{}{}
		}
	}
	ids := make(map[int]struct{})
	for _, t := range tags {
		if _, ok := want[normalizeLabel(t.Label)]; ok {
			ids[t.ID] = struct{}{}
		}
	}
	return ids
}

// UnmatchedLabels returns the labels (verbatim, in input order) that match no
// tag in tags, following the same case-insensitive, whitespace-trimmed rule as
// TagIDs. Empty or whitespace-only labels are ignored. It returns nil when
// every non-empty label matches or none is supplied, and is the companion to
// TagIDs for surfacing a misconfigured tag name.
func UnmatchedLabels(tags []Tag, labels ...string) []string {
	if len(labels) == 0 {
		return nil
	}
	have := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		have[normalizeLabel(t.Label)] = struct{}{}
	}
	var unmatched []string
	for _, l := range labels {
		norm := normalizeLabel(l)
		if norm == "" {
			continue
		}
		if _, ok := have[norm]; !ok {
			unmatched = append(unmatched, l)
		}
	}
	return unmatched
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

// normalizeLabel lowercases and trims a tag label so matching is
// case-insensitive and whitespace-insensitive (Sonarr and Radarr store labels
// lowercased). It is the single normalization used by TagIDs and
// UnmatchedLabels so the two never diverge.
func normalizeLabel(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
