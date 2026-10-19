package arrapi

import "strings"

// Tag is a server tag.
type Tag struct {
	Label string
	ID    int
}

// TagIDs returns the ids of the tags whose label matches one of labels, both compared as
// strings.ToLower(strings.TrimSpace(label)). It returns nil only when no label is given;
// given any label it returns a non-nil set, empty when nothing matches, so a caller reads
// "filter on, nothing matches" from the set itself.
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

// HasAnyTag reports whether itemTags holds any of ids.
func HasAnyTag(itemTags []int, ids map[int]struct{}) bool {
	for _, id := range itemTags {
		if _, ok := ids[id]; ok {
			return true
		}
	}
	return false
}

// normalizeLabel lowercases rather than case-folds: strings.EqualFold would also
// match distinct letters that fold together, such as ϑ and θ.
func normalizeLabel(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
